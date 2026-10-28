package schedulerprocess

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/rpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestSchedulerProcesses(t *testing.T) {
	if os.Getenv(processGateEnv) != "1" {
		t.Skip("set " + processGateEnv + "=1 to run process integration tests")
	}
	backend := os.Getenv(backendEnv)
	if backend != "postgres" && backend != "mongodb" {
		t.Fatalf("%s must be postgres or mongodb, got %q", backendEnv, backend)
	}
	if testing.Short() {
		t.Skip("scheduler process integration tests require Docker")
	}

	fixture := startDatabaseFixture(t, backend)
	t.Run("independent servers reserve capacity and run submitted tasks once", func(t *testing.T) {
		settings := fixture.newDatabase(t)
		runCapacityScenario(t, settings)
	})
	t.Run("scheduler restart recovers committed assignment before initialization", func(t *testing.T) {
		settings := fixture.newDatabase(t)
		runSchedulerCrashScenario(t, settings)
	})
	t.Run("worker restart preserves execution admission without duplicate launch", func(t *testing.T) {
		settings := fixture.newDatabase(t)
		runWorkerCrashScenario(t, settings)
	})
}

func runCapacityScenario(t *testing.T, settings dbSettings) {
	t.Helper()
	markers := filepath.Join(t.TempDir(), "markers")
	containers := newDockerContainerCleanup(t)
	if err := os.MkdirAll(filepath.Join(markers, "active"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(markers, "release"), 0755); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, settings, false)
	defer store.Close()

	rpcPorts := []string{freePort(t), freePort(t)}
	serverProcesses := make([]*childProcess, 0, len(rpcPorts))
	for _, port := range rpcPorts {
		httpPort := freePort(t)
		process := startHelper(t, settings, "server", map[string]string{
			"FUNNEL_PROCESS_HTTP_PORT": httpPort,
			"FUNNEL_PROCESS_RPC_PORT":  port,
		})
		serverProcesses = append(serverProcesses, process)
		waitForServer(t, process, port)
	}
	defer stopChildren(t, serverProcesses)

	const capacity = 2
	nodeProcesses := make([]*childProcess, 0, capacity)
	nodeIDs := []string{"process-node-a", "process-node-b"}
	for i, nodeID := range nodeIDs {
		workDir := filepath.Join(t.TempDir(), "worker")
		process := startHelper(t, settings, "node", map[string]string{
			"FUNNEL_PROCESS_RPC_PORT":      rpcPorts[i],
			"FUNNEL_PROCESS_NODE_ID":       nodeID,
			"FUNNEL_PROCESS_NODE_WORK_DIR": workDir,
			"FUNNEL_PROCESS_MARKER_DIR":    markers,
		})
		nodeProcesses = append(nodeProcesses, process)
		waitForNode(t, store, nodeID)
	}
	defer stopChildren(t, nodeProcesses)

	const taskCount = 6
	taskIDs := make([]string, taskCount)
	markerIDs := make([]string, taskCount)
	for i := range taskIDs {
		markerID := fmt.Sprintf("process-task-%02d", i)
		createdID, err := submitTask(t, rpcPorts[i%len(rpcPorts)], markerID, markers, true)
		if err != nil {
			t.Fatalf("submit task %s: %v", markerID, err)
		}
		taskIDs[i] = createdID
		markerIDs[i] = markerID
		containers.add(createdID + "-0")
	}

	waitForLaunchCount(t, markers, capacity, store, nodeIDs, taskIDs, append(serverProcesses, nodeProcesses...)...)
	assertCapacityHeld(t, markers, store, nodeIDs, taskIDs, capacity)
	initialLaunches := readLaunches(t, markers)
	if len(initialLaunches) != capacity {
		t.Fatalf("launches at full capacity = %v, want %d", initialLaunches, capacity)
	}
	assertNoLaunchWhileFull(t, markers, store, nodeIDs, taskIDs, initialLaunches, capacity)

	for len(readLaunches(t, markers)) < taskCount {
		launched := readLaunches(t, markers)
		active := readActive(t, markers)
		if len(active) != capacity {
			t.Fatalf("active tasks before releasing a slot = %v, want %d", active, capacity)
		}
		if err := os.WriteFile(filepath.Join(markers, "release", active[0]), nil, 0600); err != nil {
			t.Fatalf("release task %s: %v", active[0], err)
		}
		wantLaunches := len(launched) + 1
		waitForLaunchCount(t, markers, wantLaunches, store, nodeIDs, taskIDs, append(serverProcesses, nodeProcesses...)...)
		if got := len(readLaunches(t, markers)); got != wantLaunches {
			t.Fatalf("launch count after releasing one slot = %d, want %d", got, wantLaunches)
		}
		assertCapacityInvariant(t, store, nodeIDs, capacity)
	}

	for _, taskID := range readActive(t, markers) {
		if err := os.WriteFile(filepath.Join(markers, "release", taskID), nil, 0600); err != nil {
			t.Fatalf("release task %s: %v", taskID, err)
		}
	}
	waitForTasksComplete(t, store, taskIDs, append(serverProcesses, nodeProcesses...)...)
	assertEachTaskLaunchedOnce(t, markers, markerIDs)
	for _, taskID := range taskIDs {
		task := getTask(t, store, taskID)
		if task.GetState() != tes.State_COMPLETE {
			t.Fatalf("task %s state = %s, want COMPLETE", taskID, task.GetState())
		}
	}
}

func runSchedulerCrashScenario(t *testing.T, settings dbSettings) {
	t.Helper()
	markers := filepath.Join(t.TempDir(), "markers")
	containers := newDockerContainerCleanup(t)
	if err := os.MkdirAll(markers, 0755); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, settings, false)
	defer store.Close()
	taskID := fmt.Sprintf("committed-before-initializing-%d", time.Now().UnixNano())
	const nodeID = "scheduler-recovery-node"
	createTask(t, store, taskID, markerTask(taskID, markers, false))
	containers.add(taskID + "-0")
	seedNode(t, store, nodeID, 1)

	barrier := startHelper(t, settings, "scheduler-commit-barrier", map[string]string{
		"FUNNEL_PROCESS_NODE_ID": nodeID,
	})
	line := barrier.waitForLine(t, "PROCESS_EVENT: ASSIGNMENT_COMMITTED", 30*time.Second)
	t.Logf("scheduler boundary: %s", line)
	stopByKill(t, barrier)

	queued := getTask(t, store, taskID)
	if queued.GetState() != tes.State_QUEUED {
		t.Fatalf("task state after scheduler kill = %s, want QUEUED before initialization", queued.GetState())
	}
	owner, err := store.TryAssign(systemContext(), queued, nil)
	if err != nil || owner != nodeID {
		t.Fatalf("committed assignment owner = %q, %v; want %q", owner, err, nodeID)
	}
	node, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("read committed reservation: %v", err)
	}
	if len(node.TaskIds) != 1 || node.TaskIds[0] != taskID || node.Available.GetCpus() != 0 {
		t.Fatalf("committed reservation = tasks %v, available %v; want [%s] and 0 CPUs", node.TaskIds, node.Available, taskID)
	}

	rpcPort, httpPort := freePort(t), freePort(t)
	server := startHelper(t, settings, "server", map[string]string{
		"FUNNEL_PROCESS_HTTP_PORT": httpPort,
		"FUNNEL_PROCESS_RPC_PORT":  rpcPort,
	})
	waitForServer(t, server, rpcPort)
	defer stopChildren(t, []*childProcess{server})
	waitForTaskState(t, store, taskID, tes.State_INITIALIZING, 30*time.Second, server)

	nodeProcess := startHelper(t, settings, "node", map[string]string{
		"FUNNEL_PROCESS_RPC_PORT":      rpcPort,
		"FUNNEL_PROCESS_NODE_ID":       nodeID,
		"FUNNEL_PROCESS_NODE_WORK_DIR": filepath.Join(t.TempDir(), "worker"),
		"FUNNEL_PROCESS_MARKER_DIR":    markers,
	})
	defer stopChildren(t, []*childProcess{nodeProcess})
	waitForTaskState(t, store, taskID, tes.State_COMPLETE, 90*time.Second, server, nodeProcess)
	assertEachTaskLaunchedOnce(t, markers, []string{taskID})
}

func runWorkerCrashScenario(t *testing.T, settings dbSettings) {
	t.Helper()
	markers := filepath.Join(t.TempDir(), "markers")
	containers := newDockerContainerCleanup(t)
	if err := os.MkdirAll(markers, 0755); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, settings, false)
	defer store.Close()
	taskID := fmt.Sprintf("worker-admission-survives-kill-%d", time.Now().UnixNano())
	const nodeID = "worker-admission-node"
	createTask(t, store, taskID, markerTask(taskID, markers, false))
	containers.add(taskID + "-0")
	seedNode(t, store, nodeID, 1)
	queued := getTask(t, store, taskID)
	candidate, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("read worker reservation candidate: %v", err)
	}
	owner, err := store.TryAssign(systemContext(), queued, candidate)
	if err != nil || owner != nodeID {
		t.Fatalf("reserve worker task = (%q, %v), want (%q, nil)", owner, err, nodeID)
	}
	if err := store.WriteEvent(systemContext(), events.NewState(taskID, tes.State_INITIALIZING)); err != nil {
		t.Fatalf("initialize worker task: %v", err)
	}
	conf := baseProcessConfig(settings)
	conf.Worker.WorkDir = filepath.Join(t.TempDir(), "worker")
	conf.Worker.LeaveWorkDir = true
	configureMarkerVolume(conf, markers)

	barrier := startHelper(t, settings, "worker-admission-barrier", map[string]string{
		"FUNNEL_PROCESS_TASK_ID":       taskID,
		"FUNNEL_PROCESS_MARKER_DIR":    markers,
		"FUNNEL_PROCESS_NODE_WORK_DIR": conf.Worker.WorkDir,
	})
	line := barrier.waitForLine(t, "PROCESS_EVENT: EXECUTION_ADMITTED", 30*time.Second)
	t.Logf("worker boundary: %s", line)
	stopByKill(t, barrier)
	if task := getTask(t, store, taskID); task.GetState() != tes.State_RUNNING {
		t.Fatalf("task state after worker kill = %s, want RUNNING", task.GetState())
	}
	assertWorkerReservation(t, store, nodeID, taskID)
	if admitted, err := store.ClaimExecution(systemContext(), taskID); err != nil || admitted {
		t.Fatalf("second execution admission = %v, %v; want durable claim to reject it", admitted, err)
	}

	observer := startHelper(t, settings, "worker-observer", map[string]string{
		"FUNNEL_PROCESS_TASK_ID":       taskID,
		"FUNNEL_PROCESS_MARKER_DIR":    markers,
		"FUNNEL_PROCESS_NODE_WORK_DIR": conf.Worker.WorkDir,
	})
	observer.waitForLine(t, "PROCESS_EVENT: RUNNING_OBSERVED", 20*time.Second)
	assertWorkerReservation(t, store, nodeID, taskID)
	if err := waitForMarkerQuiet(t, markers, 750*time.Millisecond, observer); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteEvent(systemContext(), events.NewState(taskID, tes.State_CANCELED)); err != nil {
		t.Fatalf("cancel task to release observer: %v", err)
	}
	if err := observer.wait(t, 10*time.Second); err != nil {
		t.Fatalf("observer worker did not exit after task cancellation: %v\n%s", err, observer.diagnostics())
	}
	if launches := readLaunches(t, markers); len(launches) != 0 {
		t.Fatalf("executor launches after worker restart = %v, want none", launches)
	}
}

func assertWorkerReservation(t *testing.T, store schedulerStore, nodeID, taskID string) {
	t.Helper()
	node, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("read node %s persisted reservation: %v", nodeID, err)
	}
	if len(node.TaskIds) != 1 || node.TaskIds[0] != taskID || node.Available.GetCpus() != 0 {
		t.Fatalf("node %s reservation after worker restart = tasks %v, available %v; want [%s] and 0 CPUs", nodeID, node.TaskIds, node.Available, taskID)
	}
}

func createTask(t *testing.T, store schedulerStore, id string, task *tes.Task) {
	t.Helper()
	if task.Id == "" {
		task.Id = id
	}
	if err := store.WriteEvent(systemContext(), events.NewTaskCreated(task)); err != nil {
		t.Fatalf("create task %s: %v", id, err)
	}
}

func seedNode(t *testing.T, store schedulerStore, id string, cpus uint32) {
	t.Helper()
	_, err := store.PutNode(systemContext(), &scheduler.Node{
		Id: id, State: scheduler.NodeState_ALIVE,
		Resources: &scheduler.Resources{Cpus: cpus, RamGb: 2, DiskGb: 4},
	})
	if err != nil {
		t.Fatalf("seed node %s: %v", id, err)
	}
}

func submitTask(t *testing.T, port, id, markers string, hold bool) (string, error) {
	t.Helper()
	conf := config.DefaultConfig()
	conf.RPCClient.ServerAddress = net.JoinHostPort("127.0.0.1", port)
	conf.RPCClient.Timeout = &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(10 * time.Second)}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := rpc.Dial(ctx, conf.RPCClient)
	if err != nil {
		return "", fmt.Errorf("dial scheduler server: %w", err)
	}
	defer conn.Close()
	response, err := tes.NewTaskServiceClient(conn).CreateTask(ctx, markerTask(id, markers, hold))
	if err != nil {
		return "", err
	}
	return response.GetId(), nil
}

func markerTask(id, markers string, hold bool) *tes.Task {
	command := fmt.Sprintf("mkdir -p /funnel-process-test/active /funnel-process-test/release; touch /funnel-process-test/active/%s; echo %s >> /funnel-process-test/launches", id, id)
	if hold {
		command += fmt.Sprintf("; while [ ! -f /funnel-process-test/release/%s ]; do sleep 0.05; done", id)
	}
	command += fmt.Sprintf("; rm -f /funnel-process-test/active/%s", id)
	return &tes.Task{
		Id: id, Name: id,
		Executors: []*tes.Executor{{Image: "alpine:3.21", Command: []string{"sh", "-c", command}}},
		Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1},
	}
}

func waitForLaunchCount(t *testing.T, markers string, count int, store schedulerStore, nodeIDs, taskIDs []string, processes ...*childProcess) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if diagnostics := processFailure(processes...); diagnostics != "" {
			t.Fatalf("scheduler process exited before launch count %d\n%s", count, diagnostics)
		}
		if got := len(readLaunches(t, markers)); got >= count {
			if got > count {
				t.Fatalf("executor launch count = %d, want %d\n%s", got, count, processFailure(processes...))
			}
			assertCapacityInvariant(t, store, nodeIDs, 2)
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("only %d tasks launched, want %d\nlaunches=%v active=%v\ndatabase=%s\n%s", len(readLaunches(t, markers)), count, readLaunches(t, markers), readActive(t, markers), databaseSnapshot(store, nodeIDs, taskIDs), processFailure(processes...))
}

func databaseSnapshot(store schedulerStore, nodeIDs, taskIDs []string) string {
	var snapshot []string
	for _, taskID := range taskIDs {
		task, err := store.GetTask(systemContext(), &tes.GetTaskRequest{Id: taskID})
		if err != nil {
			snapshot = append(snapshot, fmt.Sprintf("task %s: %v", taskID, err))
		} else {
			snapshot = append(snapshot, fmt.Sprintf("task %s=%s", taskID, task.GetState()))
		}
	}
	for _, nodeID := range nodeIDs {
		node, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: nodeID})
		if err != nil {
			snapshot = append(snapshot, fmt.Sprintf("node %s: %v", nodeID, err))
		} else {
			snapshot = append(snapshot, fmt.Sprintf("node %s=%v", nodeID, node))
		}
	}
	return strings.Join(snapshot, "; ")
}

func assertNoLaunchWhileFull(t *testing.T, markers string, store schedulerStore, nodeIDs, taskIDs, before []string, capacity int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		assertCapacityHeld(t, markers, store, nodeIDs, taskIDs, capacity)
		if got := readLaunches(t, markers); !equalStrings(got, before) {
			t.Fatalf("queued work launched while all slots were held: before=%v after=%v", before, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertCapacityHeld(t *testing.T, markers string, store schedulerStore, nodeIDs, taskIDs []string, capacity int) {
	t.Helper()
	active := readActive(t, markers)
	if len(active) != capacity {
		t.Fatalf("active executor count = %d, want all %d slots occupied; active=%v", len(active), capacity, active)
	}
	assertCapacityInvariant(t, store, nodeIDs, capacity)
	submitted := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		submitted[taskID] = struct{}{}
	}
	reserved := make(map[string]struct{}, capacity)
	for _, nodeID := range nodeIDs {
		node, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: nodeID})
		if err != nil {
			t.Fatalf("read full node %s: %v", nodeID, err)
		}
		if node.Resources.GetCpus() != 1 || len(node.TaskIds) != 1 || node.Available.GetCpus() != 0 {
			t.Fatalf("full node %s has resources %v, reservations %v, available %v; want 1 CPU, one task, and 0 available CPUs", nodeID, node.Resources, node.TaskIds, node.Available)
		}
		taskID := node.TaskIds[0]
		if _, ok := submitted[taskID]; !ok {
			t.Fatalf("node %s reserved unsubmitted task %s", nodeID, taskID)
		}
		if _, duplicate := reserved[taskID]; duplicate {
			t.Fatalf("task %s is reserved on more than one node", taskID)
		}
		reserved[taskID] = struct{}{}
		if state := getTask(t, store, taskID).GetState(); state != tes.State_RUNNING {
			t.Fatalf("reserved task %s state = %s, want RUNNING while its executor marker is active", taskID, state)
		}
	}
	if len(reserved) != capacity {
		t.Fatalf("active reservations = %d, want all %d node slots occupied", len(reserved), capacity)
	}
	queued := 0
	for _, id := range taskIDs {
		if getTask(t, store, id).GetState() == tes.State_QUEUED {
			queued++
		}
	}
	if queued == 0 {
		t.Fatal("all tasks left QUEUED while the test expected pending work")
	}
}

func assertCapacityInvariant(t *testing.T, store schedulerStore, nodeIDs []string, capacity int) {
	t.Helper()
	reserved := 0
	for _, id := range nodeIDs {
		node, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: id})
		if err != nil {
			t.Fatalf("read node %s capacity: %v", id, err)
		}
		if node.Available.GetCpus() > node.Resources.GetCpus() {
			t.Fatalf("node %s available CPUs %d exceed capacity %d", id, node.Available.GetCpus(), node.Resources.GetCpus())
		}
		if len(node.TaskIds) > int(node.Resources.GetCpus()) {
			t.Fatalf("node %s has %d active reservations for %d CPUs: %v", id, len(node.TaskIds), node.Resources.GetCpus(), node.TaskIds)
		}
		reserved += len(node.TaskIds)
	}
	if reserved > capacity {
		t.Fatalf("active node reservations = %d, exceed total capacity %d", reserved, capacity)
	}
}

func readLaunches(t *testing.T, markers string) []string {
	t.Helper()
	path := filepath.Join(markers, "launches")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read launch markers: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func readActive(t *testing.T, markers string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(markers, "active"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read active executor markers: %v", err)
	}
	active := make([]string, 0, len(entries))
	for _, entry := range entries {
		active = append(active, entry.Name())
	}
	sort.Strings(active)
	return active
}

func assertEachTaskLaunchedOnce(t *testing.T, markers string, expected []string) {
	t.Helper()
	got := readLaunches(t, markers)
	counts := make(map[string]int, len(got))
	for _, id := range got {
		counts[id]++
	}
	for _, id := range expected {
		if counts[id] != 1 {
			t.Errorf("task %s launch count = %d, want 1; launches=%v", id, counts[id], got)
		}
	}
	if len(got) != len(expected) {
		t.Errorf("total executor launches = %d, want %d; launches=%v", len(got), len(expected), got)
	}
}

func waitForTasksComplete(t *testing.T, store schedulerStore, ids []string, processes ...*childProcess) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if diagnostics := processFailure(processes...); diagnostics != "" {
			t.Fatalf("scheduler process exited before tasks completed\n%s", diagnostics)
		}
		complete := true
		for _, id := range ids {
			if getTask(t, store, id).GetState() != tes.State_COMPLETE {
				complete = false
			}
		}
		if complete {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("tasks did not all complete; states=%v\n%s", taskStates(t, store, ids), processFailure(processes...))
}

func waitForTaskState(t *testing.T, store schedulerStore, id string, want tes.State, timeout time.Duration, processes ...*childProcess) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if diagnostics := processFailure(processes...); diagnostics != "" {
			t.Fatalf("process exited before task %s reached %s\n%s", id, want, diagnostics)
		}
		if got := getTask(t, store, id).GetState(); got == want {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s; state=%s\n%s", id, want, getTask(t, store, id).GetState(), processFailure(processes...))
}

func waitForMarkerQuiet(t *testing.T, markers string, duration time.Duration, process *childProcess) error {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if err := process.exitError(); err != nil {
			return fmt.Errorf("worker observer exited early: %v\n%s", err, process.diagnostics())
		}
		if launches := readLaunches(t, markers); len(launches) != 0 {
			return fmt.Errorf("restarted worker launched executor unexpectedly: %v\n%s", launches, process.diagnostics())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func getTask(t *testing.T, store schedulerStore, id string) *tes.Task {
	t.Helper()
	task, err := store.GetTask(systemContext(), &tes.GetTaskRequest{Id: id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatalf("read task %s: %v", id, err)
	}
	return task
}

func taskStates(t *testing.T, store schedulerStore, ids []string) map[string]tes.State {
	t.Helper()
	states := make(map[string]tes.State, len(ids))
	for _, id := range ids {
		states[id] = getTask(t, store, id).GetState()
	}
	return states
}

func systemContext() context.Context {
	return context.WithValue(context.Background(), server.UserInfoKey, &server.UserInfo{IsAdmin: true})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
