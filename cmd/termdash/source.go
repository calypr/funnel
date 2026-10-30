package termdash

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util"
	"google.golang.org/protobuf/encoding/protojson"
)

type taskClient interface {
	ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error)
	GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error)
	CancelTask(context.Context, *tes.CancelTaskRequest) (*tes.CancelTaskResponse, error)
}

// TaskSource fetches tasks from a Funnel server, tracking pagination state.
type TaskSource struct {
	client     taskClient
	pageSize   int32
	pPage      []string
	cPage      string
	nPage      string
	lock       sync.RWMutex
	nodeURL    string
	nodeClient *http.Client
}

type taskWorker struct {
	TaskID   string            `json:"task_id"`
	State    string            `json:"task_state"`
	Hostname string            `json:"hostname"`
	Metadata map[string]string `json:"metadata"`
}

func (ts *TaskSource) TaskWorkers() ([]taskWorker, error) {
	var workers []taskWorker
	for _, state := range []tes.State{tes.State_RUNNING, tes.State_INITIALIZING} {
		token := ""
		seen := make(map[string]bool)
		for {
			page, err := ts.client.ListTasks(context.Background(), &tes.ListTasksRequest{
				View: tes.View_BASIC.String(), State: state, PageSize: 256, PageToken: token,
			})
			if err != nil {
				return nil, fmt.Errorf("fetch recorded task workers: %w", err)
			}
			for _, task := range page.Tasks {
				logs := task.GetLogs()
				if len(logs) == 0 {
					continue
				}
				metadata := logs[len(logs)-1].GetMetadata()
				hostname := metadata["hostname"]
				if hostname == "" {
					continue
				}
				workers = append(workers, taskWorker{TaskID: task.Id, State: task.State.String(), Hostname: hostname, Metadata: metadata})
			}
			if page.NextPageToken == "" {
				break
			}
			if seen[page.NextPageToken] {
				return nil, fmt.Errorf("task server returned a repeated page token")
			}
			seen[page.NextPageToken] = true
			token = page.NextPageToken
		}
	}
	return workers, nil
}

func NewTaskSource(tesHTTPServerAddress string, pageSize int32) (*TaskSource, error) {
	cli, err := tes.NewClient(tesHTTPServerAddress)
	if err != nil {
		return nil, err
	}
	address := tesHTTPServerAddress
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return &TaskSource{client: cli, pageSize: pageSize, nodeURL: strings.TrimSuffix(u.String(), "/"), nodeClient: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (ts *TaskSource) Cancel(id string) error {
	_, err := ts.client.CancelTask(context.Background(), &tes.CancelTaskRequest{Id: id})
	return err
}

func (ts *TaskSource) Nodes() ([]*scheduler.Node, error) {
	var nodes []*scheduler.Node
	token := ""
	seen := make(map[string]bool)
	for {
		u, err := url.Parse(ts.nodeURL + "/v1/nodes")
		if err != nil {
			return nil, err
		}
		query := u.Query()
		query.Set("page_size", "500")
		if token != "" {
			query.Set("page_token", token)
		}
		u.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(os.Getenv("FUNNEL_SERVER_USER"), os.Getenv("FUNNEL_SERVER_PASSWORD"))
		resp, err := ts.nodeClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch scheduler nodes: %w", err)
		}
		if resp.StatusCode == http.StatusNotFound {
			_, closeErr := util.CheckHTTPResponse(resp, nil)
			return nil, fmt.Errorf("scheduler node API is unavailable for this deployment: %w", closeErr)
		}
		body, err := util.CheckHTTPResponse(resp, nil)
		if err != nil {
			return nil, fmt.Errorf("fetch scheduler nodes: %w", err)
		}
		page := &scheduler.ListNodesResponse{}
		err = protojson.Unmarshal(body, page)
		if err != nil {
			return nil, fmt.Errorf("decode scheduler nodes: %w", err)
		}
		nodes = append(nodes, page.Nodes...)
		if page.NextPageToken == "" {
			return nodes, nil
		}
		if seen[page.NextPageToken] {
			return nil, fmt.Errorf("scheduler returned a repeated node page token")
		}
		seen[page.NextPageToken] = true
		token = page.NextPageToken
	}
}

// listTasks fetches a raw page and commits pagination state after a successful
// response.
func (ts *TaskSource) listTasks(previous, next bool) ([]*tes.Task, error) {
	ts.lock.RLock()
	pPage := append([]string(nil), ts.pPage...)
	cPage := ts.cPage
	nPage := ts.nPage
	ts.lock.RUnlock()

	if next && !previous {
		if nPage != "" {
			if cPage != "" {
				pPage = append(pPage, cPage)
			}
			cPage = nPage
		}
	} else if previous && !next {
		if len(pPage) > 0 {
			cPage = pPage[len(pPage)-1]
			pPage = pPage[:len(pPage)-1]
		} else {
			cPage = ""
		}
	}

	resp, err := ts.client.ListTasks(context.Background(), &tes.ListTasksRequest{
		View:      tes.View_BASIC.String(),
		PageSize:  ts.pageSize,
		PageToken: cPage,
	})
	if err != nil {
		return nil, err
	}

	ts.lock.Lock()
	ts.pPage = pPage
	ts.cPage = cPage
	ts.nPage = resp.NextPageToken
	ts.lock.Unlock()
	return resp.Tasks, nil
}

// List returns the current page of tasks, sorted and filtered.
func (ts *TaskSource) List(previous, next bool, sortField string, sortReversed, allTasks bool, filter string) ([]*tes.Task, error) {
	tasks, err := ts.listTasks(previous, next)
	if err != nil {
		return nil, err
	}

	tasks = sortTasks(tasks, sortField, sortReversed)
	tasks = filterTasks(tasks, allTasks, filter)
	return tasks, nil
}

// Get fetches a single task with its full view.
func (ts *TaskSource) Get(id string) (*tes.Task, error) {
	return ts.client.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_FULL.String(),
	})
}

func (ts *TaskSource) NextPageToken() string {
	ts.lock.RLock()
	defer ts.lock.RUnlock()
	return ts.nPage
}

func (ts *TaskSource) PreviousPageToken() string {
	ts.lock.RLock()
	defer ts.lock.RUnlock()
	if len(ts.pPage) > 0 {
		return ts.pPage[len(ts.pPage)-1]
	} else if ts.cPage != "" {
		return ts.cPage
	}
	return ""
}

// Sorters maps sort field names to comparison functions.
type sortMethod func(c1, c2 *tes.Task) bool

var Sorters = map[string]sortMethod{
	"id": func(c1, c2 *tes.Task) bool { return c1.Id < c2.Id },
	"state": func(c1, c2 *tes.Task) bool {
		if c1.State.String() == c2.State.String() {
			return c1.Name < c2.Name
		}
		return c1.State.String() < c2.State.String()
	},
	"name":        func(c1, c2 *tes.Task) bool { return c1.Name < c2.Name },
	"description": func(c1, c2 *tes.Task) bool { return c1.Description < c2.Description },
}

func SortFields() []string {
	fields := make([]string, 0, len(Sorters))
	for k := range Sorters {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields
}

func sortTasks(tasks []*tes.Task, field string, reversed bool) []*tes.Task {
	f, ok := Sorters[field]
	if !ok {
		f = Sorters["id"]
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if reversed {
			return f(tasks[j], tasks[i])
		}
		return f(tasks[i], tasks[j])
	})
	return tasks
}

func filterTasks(tasks []*tes.Task, allTasks bool, filter string) []*tes.Task {
	if allTasks || filter == "" {
		return tasks
	}
	var out []*tes.Task
	for _, t := range tasks {
		if matchTask(t, filter) {
			out = append(out, t)
		}
	}
	return out
}

func matchTask(t *tes.Task, filter string) bool {
	return strings.Contains(t.Id, filter) || strings.Contains(t.Name, filter) ||
		strings.Contains(t.State.String(), filter) || strings.Contains(t.Description, filter)
}
