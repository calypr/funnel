package server

import (
	"encoding/gob"
	"fmt"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/plugins/proto"
	"github.com/calypr/funnel/plugins/shared"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/server"
	"github.com/calypr/funnel/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	googleproto "google.golang.org/protobuf/proto"

	"google.golang.org/grpc"
)

// TaskService is a wrapper which handles common TES Task Service operations,
// such as initializing a task when CreateTask is called. The TaskService is backed by
// two parts: a read API which provides the GetTask and ListTasks endpoints, and a write
// API which implements the events.Writer interface. Task creation and cancelation is
// managed by writing events to underlying event writer.
//
// This makes it easier to define task service backends for new databases, and ensures
// that common operations are handled consistently, such as setting IDs, handling 404s,
// GetServiceInfo, etc.
type TaskService struct {
	tes.UnimplementedTaskServiceServer
	Name          string
	Event         events.Writer
	Compute       events.Computer
	Read          tes.ReadOnlyServer
	Log           *logger.Logger
	Config        *config.Config
	Plugin        shared.Authorize
	PluginManager *shared.Manager

	// submitWG tracks in-flight compute backend submissions started by
	// CreateTask; Close waits for them so a server shutdown can't strand
	// a partially created backend job.
	submitWG sync.WaitGroup
}

// Close waits (bounded) for in-flight compute backend submissions.
func (ts *TaskService) Close() {
	done := make(chan struct{})
	go func() {
		ts.submitWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		ts.Log.Warn("timed out waiting for in-flight task submissions to complete")
	}
}

func (ts *TaskService) forbiddenPathPrefixes() []string {
	if ts.Config == nil || (strings.ToLower(strings.TrimSpace(ts.Config.Compute)) != "kubernetes" && strings.ToLower(strings.TrimSpace(ts.Config.Compute)) != "k8s") || ts.Config.Kubernetes == nil {
		return nil
	}
	return ts.Config.Kubernetes.GetForbiddenPathPrefixes()
}

func (ts *TaskService) mountExecutorBackend(ctx context.Context) string {
	if ts.Config == nil {
		return ""
	}
	compute := ts.Config.Compute
	if pluginResponse, ok := ctx.Value("pluginResponse").(*proto.JobResponse); ok && pluginResponse.Config != nil && pluginResponse.Config.Compute != "" {
		compute = pluginResponse.Config.Compute
	}
	switch strings.ToLower(strings.TrimSpace(compute)) {
	case "local", "manual", "aws-batch", "aws", "amazon", "gridengine", "htcondor", "pbs", "slurm":
		return "docker"
	case "kubernetes", "k8s":
		return "kubernetes"
	default:
		return ""
	}
}

type contextKey string

const InternalCallKey contextKey = "internalCall"

// LoadPlugins loads plugins for a task.
func (ts *TaskService) DoPluginAction(ctx context.Context, task *tes.Task, taskType proto.Type) (*proto.JobResponse, error) {
	header := map[string]*proto.StringList{}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("Headers not passed from context")
	}
	for k, v := range md {
		// Some special headers start with ':' and cause downstream errors if kept
		if !strings.HasPrefix(k, ":") {
			header[k] = &proto.StringList{Values: v}
		}
	}
	resp, err := ts.Plugin.PluginAction(ts.Config.Plugins.Params, header, ts.Config, task, taskType)
	if err != nil {
		return resp, fmt.Errorf("DoPluginAction: PluginAction failed: %w", err)
	}
	return resp, nil
}

func (ts *TaskService) HandleDoPluginAction(ctx context.Context, task *tes.Task, taskType proto.Type) (*proto.JobResponse, error) {
	gob.Register(&config.TimeoutConfig_Duration{})
	gob.Register(&config.TimeoutConfig_Disabled{})

	pluginResponse, err := ts.DoPluginAction(ctx, task, taskType)
	if err != nil {
		return pluginResponse, fmt.Errorf("Error loading plugins: %v", err)
	}
	if pluginResponse.Code != 200 {
		return pluginResponse, fmt.Errorf(
			"Plugin returned error: code: %d, message: %s, user: %s, task: %v",
			pluginResponse.Code,
			pluginResponse.Message,
			pluginResponse.UserId,
			pluginResponse.Task,
		)
	}
	if pluginResponse.Config == nil {
		return pluginResponse, fmt.Errorf("Plugin returned empty config")
	}
	return pluginResponse, err
}

// CreateTask provides an HTTP/gRPC endpoint for creating a task.
// This is part of the TES implementation.
func (ts *TaskService) CreateTask(ctx context.Context, task *tes.Task) (*tes.CreateTaskResponse, error) {
	if ts.Config.Plugins != nil {
		pluginResponse, err := ts.HandleDoPluginAction(ctx, task, proto.Type_CREATE)
		if err != nil {
			if pluginResponse != nil && pluginResponse.Code != 0 {
				return nil, status.Errorf(server.GRPCCodeFromHTTPStatus(int(pluginResponse.Code)), "%v", err.Error())
			} else {
				return nil, err
			}
		}
		ts.Log.Debug("Plugin", "Response Code", pluginResponse.Code,
			"Message", pluginResponse.Message,
			"User", pluginResponse.UserId,
			"Task", pluginResponse.Task,
			"Config", pluginResponse.Config.Safe(),
		)
		ctx = context.WithValue(ctx, "pluginResponse", pluginResponse)

		// If using plugin, replace existing task with returned task from plugin
		if pluginResponse.Task != nil {
			task = pluginResponse.Task
		}
	}

	if err := tes.InitTaskWithForbiddenPathPrefixes(task, true, ts.forbiddenPathPrefixes()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err.Error())
	}
	if executorBackend := ts.mountExecutorBackend(ctx); executorBackend != "" {
		if err := tes.ValidateMountPaths(task, executorBackend); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	if err := tes.NormalizeBackendParameters(task.GetResources()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if backend, ok := ts.Compute.(interface{ BackendParameterKeys() []string }); ok {
		unsupported, err := tes.FilterBackendParameters(task.GetResources(), backend.BackendParameterKeys())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if len(unsupported) > 0 && ts.Log != nil {
			ts.Log.Warn("ignoring unsupported backend parameters", "keys", unsupported)
		}
	}
	if err := ts.Compute.CheckBackendParameterSupport(task); err != nil {
		return nil, err
	}

	var err error
	ctx = context.WithValue(ctx, "Config", ts.Config)

	if ts.Config.Compute == "kubernetes" {
		task.Resources, err = config.ValidateResources(task.Resources, ts.Config.Kubernetes.Resources)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid resources: %v", err)
		}
	}

	if err := ts.Event.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		return nil, fmt.Errorf("error creating task: %s", err)
	}

	pluginResponse := ctx.Value("pluginResponse")
	conf := ctx.Value("Config")

	// dispatch to compute backend
	ts.submitWG.Add(1)
	go func() {
		defer ts.submitWG.Done()
		// Bounded context: a hung compute backend must not pin this goroutine
		// (and the resources it has partially created) forever.
		workerCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		if pluginResponse != nil {
			workerCtx = context.WithValue(workerCtx, "pluginResponse", pluginResponse)
		}
		if conf != nil {
			workerCtx = context.WithValue(workerCtx, "Config", conf)
		}

		err := ts.Compute.WriteEvent(workerCtx, events.NewTaskCreated(task))
		if err != nil {
			ts.Log.Error("error submitting task to compute backend", "taskID", task.Id, "error", err)
			failureCtx, cancelFailure := context.WithTimeout(context.WithoutCancel(workerCtx), 10*time.Second)
			defer cancelFailure()
			if logErr := ts.Event.WriteEvent(failureCtx, events.NewSystemLog(
				task.Id, 0, 0, "error",
				"Error submitting task to compute backend",
				map[string]string{"error": err.Error()},
			)); logErr != nil {
				ts.Log.Error("failed to write system log for compute backend submission error", "taskID", task.Id, "error", logErr)
			}
			if err := ts.Event.WriteEvent(failureCtx, events.NewState(task.Id, tes.SystemError)); err != nil {
				ts.Log.Error("error writing SystemError event after compute backend submission failure", "taskID", task.Id, "error", err)
			}
		} else {
			ts.Log.Debug("submitted task to compute backend", "taskID", task.Id)
		}
	}()

	return &tes.CreateTaskResponse{Id: task.Id}, nil
}

func normalizeTaskView(view string) (string, error) {
	if view == "" {
		return tes.View_MINIMAL.String(), nil
	}
	normalized := strings.ToUpper(view)
	if _, ok := tes.View_value[normalized]; !ok {
		return "", status.Errorf(codes.InvalidArgument, "invalid view %q: must be one of MINIMAL, BASIC, FULL", view)
	}
	return normalized, nil
}

// GetTask calls GetTask on the underlying tes.ReadOnlyServer. If the underlying server
// returns tes.ErrNotFound, TaskService will handle returning the appropriate gRPC error.
func (ts *TaskService) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	/*
			TODO: This function gets called internally via the GRPC client in many places.
			To make this work with the plugin, would have to reconfigure the code to bipass the client
			and talk directly to the underlying dbs.

		if ts.Config.Plugins != nil {
			ts.Log.Info("External GetTask request", "taskID", req.Id)
			pluginResponse, err := ts.HandleDoPluginAction(ctx, &tes.Task{Id: req.Id}, proto.Type_GET)
			if err != nil {
				ts.Log.Error("Plugin authorization failed", "taskID", req.Id, "error", err)
				return nil, err
			}
			ts.Log.Debug("Get Task Response: ", pluginResponse)
			ctx = context.WithValue(ctx, "pluginResponse", pluginResponse)
		} else {
			ts.Log.Debug("Internal GetTask request, skipping plugin authorization", "taskID", req.Id)
		}
	*/

	view, err := normalizeTaskView(req.View)
	if err != nil {
		return nil, err
	}
	if view != req.View {
		req = googleproto.Clone(req).(*tes.GetTaskRequest)
		req.View = view
	}
	task, err := ts.Read.GetTask(ctx, req)
	if err == tes.ErrNotFound {
		err = status.Errorf(codes.NotFound, "%v: taskID: %s", err.Error(), req.Id)
	} else if err == tes.ErrNotPermitted {
		err = status.Errorf(codes.PermissionDenied, "%v: taskID: %s", err.Error(), req.Id)
	}
	if err == nil && googleproto.Size(task) > ts.responseBytes() {
		return nil, status.Errorf(codes.ResourceExhausted, "task %q exceeds Server.MaxResponseBytes; request MINIMAL view or increase the configured response limit and the gRPC client's receive limit", req.Id)
	}
	return task, err
}

// ListTasks calls ListTasks on the underlying tes.ReadOnlyServer.
func (ts *TaskService) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	view, err := normalizeTaskView(req.View)
	if err != nil {
		return nil, err
	}
	if view != req.View {
		req = googleproto.Clone(req).(*tes.ListTasksRequest)
		req.View = view
	}
	limit := ts.responseBytes()
	if limit <= 0 {
		return nil, status.Error(codes.Internal, "Server.MaxResponseBytes is too small")
	}
	ctx = tes.WithTaskPageBytes(ctx, limit)
	return ts.Read.ListTasks(ctx, req)
}

// CancelTask cancels a task
func (ts *TaskService) CancelTask(ctx context.Context, req *tes.CancelTaskRequest) (*tes.CancelTaskResponse, error) {
	result := &tes.CancelTaskResponse{}
	if ts.Config.Plugins != nil {
		pluginResponse, err := ts.HandleDoPluginAction(ctx, nil, proto.Type_CANCEL)
		if err != nil {
			if pluginResponse != nil && pluginResponse.Code != 0 {
				return nil, status.Errorf(server.GRPCCodeFromHTTPStatus(int(pluginResponse.Code)), "%v", err.Error())
			} else {
				return nil, err
			}
		}
		ts.Log.Debug("Plugin", "Response Code", pluginResponse.Code,
			"Message", pluginResponse.Message,
			"User", pluginResponse.UserId,
			"Task", pluginResponse.Task,
			"Config", pluginResponse.Config.Safe(),
		)
		ctx = context.WithValue(ctx, "pluginResponse", pluginResponse)
	}

	// Get current task state to check if it's already terminal
	task, err := ts.Read.GetTask(ctx, &tes.GetTaskRequest{
		Id: req.Id,
	})
	if err == tes.ErrNotFound {
		return result, status.Errorf(codes.NotFound, "%v: taskID: %s", err.Error(), req.Id)
	} else if err == tes.ErrNotPermitted {
		return result, status.Errorf(codes.PermissionDenied, "%v: taskID: %s", err.Error(), req.Id)
	} else if err != nil {
		return result, err
	}

	// Check if task is already in a terminal state
	if tes.TerminalState(task.State) {
		ts.Log.Info("Task already in terminal state, skipping cancel",
			"taskId", req.Id,
			"state", task.State)

		// Return success with informational message via metadata
		msg := fmt.Sprintf("Task is already in %s state, no action needed", task.State)
		if err := grpc.SetHeader(ctx, metadata.Pairs("X-Funnel-Message", msg)); err != nil {
			ts.Log.Error("Failed to set gRPC header", "error", err)
		}
		return result, nil
	}

	// updated database and other event streams (includes access-checking)
	err = ts.Event.WriteEvent(ctx, events.NewState(req.Id, tes.Canceled))
	if err == tes.ErrNotFound {
		return result, status.Errorf(codes.NotFound, "%v: taskID: %s", err.Error(), req.Id)
	} else if err == tes.ErrNotPermitted {
		return result, status.Errorf(codes.PermissionDenied, "%v: taskID: %s", err.Error(), req.Id)
	} else if err != nil {
		return result, err
	}

	// Dispatch to the compute backend to clean up underlying resources (K8s jobs,
	// PVs, etc.). The task is already marked Canceled in the database above, so
	// cleanup is best-effort: a failure here (e.g. a resource already gone, or a
	// transient API error) must not fail the cancel request with a 500. Record the
	// failure in the task's system logs instead so it remains visible.
	if err := ts.Compute.WriteEvent(ctx, events.NewState(req.Id, tes.Canceled)); err != nil {
		ts.Log.Error("compute backend failed to clean up resources during cancel", "taskID", req.Id, "error", err)
		if logErr := ts.Event.WriteEvent(ctx, events.NewSystemLog(
			req.Id, 0, 0, "error",
			"Error cleaning up compute resources during cancel",
			map[string]string{"error": err.Error()},
		)); logErr != nil {
			ts.Log.Error("failed to write system log for cancel cleanup error", "taskID", req.Id, "error", logErr)
		}
	}

	return result, nil
}

// GetServiceInfo returns service metadata.
func (ts *TaskService) GetServiceInfo(ctx context.Context, info *tes.GetServiceInfoRequest) (*tes.ServiceInfo, error) {
	resp := &tes.ServiceInfo{
		CreatedAt:        "2016-03-21T16:27:49-07:00",
		ContactUrl:       "https://github.com/calypr/",
		Description:      "Funnel is a toolkit for distributed task execution via a simple, standard API.",
		DocumentationUrl: "https://github.com/calypr/funnel",
		Environment:      "development",
		Id:               "org.ga4gh.funnel",
		Name:             ts.Name,
		Organization: map[string]string{
			"name": "CALYPR Data Platform",
			"url":  "https://github.com/calypr/",
		},
		Storage: []string{
			"file:///path/to/local/funnel-storage",
			"s3://ohsu-compbio-funnel/storage",
		},
		TesResourcesBackendParameters: []string{},
		Type: &tes.ServiceType{
			Artifact: "tes",
			Group:    "org.ga4gh",
			Version:  "1.1.0",
		},
		UpdatedAt: time.Now().Format(time.RFC3339),
		Version:   version.Version,
	}

	if backend, ok := ts.Compute.(interface{ BackendParameterKeys() []string }); ok {
		resp.TesResourcesBackendParameters = backend.BackendParameterKeys()
	}

	/*
		//Task metrics no longer in service info as of TES 1.1
		if c, ok := ts.Read.(metrics.TaskStateCounter); ok {
			resp.TaskStateCounts = make(map[string]int32)
			// Ensure that all states are present in the response, even if zero.
			for key := range tes.State_value {
				resp.TaskStateCounts[key] = 0
			}
			cs, err := c.TaskStateCounts(ctx)
			if err != nil {
				ts.Log.Error("counting task states", "error", err)
			}
			// Override the zero values in the response.
			for key, count := range cs {
				resp.TaskStateCounts[key] = count
			}
		}
	*/

	return resp, nil
}

func (ts *TaskService) responseBytes() int {
	if ts.Config == nil {
		return tes.DefaultResponseBytes
	}
	return ts.Config.Server.ResponseBytes()
}
