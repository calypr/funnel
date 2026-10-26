package termdash

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/funnel/tes"
)

type taskClient interface {
	ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error)
	GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error)
}

// TaskSource fetches tasks from a Funnel server, tracking pagination state.
type TaskSource struct {
	client   taskClient
	pageSize int32
	pPage    []string
	cPage    string
	nPage    string
	lock     sync.RWMutex
}

func NewTaskSource(tesHTTPServerAddress string, pageSize int32) (*TaskSource, error) {
	cli, err := tes.NewClient(tesHTTPServerAddress)
	if err != nil {
		return nil, err
	}
	return &TaskSource{client: cli, pageSize: pageSize}, nil
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
