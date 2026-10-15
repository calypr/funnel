package termdash

import (
	"context"
	"sort"
	"sync"

	"github.com/ohsu-comp-bio/funnel/tes"
)

type TesTaskSource interface {
	List(bool, bool) (TaskWidgets, error)
	Get(string) (*TaskWidget, error)
	GetNextPage() string
	GetPreviousPage() string
}

type TaskSource struct {
	client   *tes.Client
	pageSize int32
	pPage    []string
	cPage    string
	nPage    string
	tasks    TaskWidgets
	lock     sync.RWMutex
}

func NewTaskSource(tesHTTPServerAddress string, pageSize int32) (*TaskSource, error) {
	// init funnel http client
	cli, err := tes.NewClient(tesHTTPServerAddress)
	if err != nil {
		return nil, err
	}
	ts := &TaskSource{
		client:   cli,
		pageSize: pageSize,
		lock:     sync.RWMutex{},
	}
	ts.tasks, _ = ts.listTasks(false, false)
	return ts, nil
}

func (ts *TaskSource) listTasks(previous, next bool) (TaskWidgets, error) {
	var tasks TaskWidgets

	if next && !previous {
		if ts.nPage != "" {
			if ts.cPage != "" {
				ts.pPage = append(ts.pPage, ts.cPage)
			}
			ts.cPage = ts.nPage
		}
	} else if previous && !next {
		if len(ts.pPage) > 0 {
			ts.cPage = ts.pPage[len(ts.pPage)-1]
			ts.pPage = ts.pPage[:len(ts.pPage)-1]
		} else {
			ts.cPage = ""
		}
	}

	resp, err := ts.client.ListTasks(context.Background(), &tes.ListTasksRequest{
		View:      tes.View_BASIC.String(),
		PageSize:  ts.pageSize,
		PageToken: ts.cPage,
	})
	if err != nil {
		return tasks, err
	}

	ts.nPage = resp.NextPageToken

	for _, t := range resp.Tasks {
		tasks = append(tasks, NewTaskWidget(t))
	}

	return tasks, nil
}

// Return array of tasks, sorted by field
func (ts *TaskSource) List(previous, next bool) (TaskWidgets, error) {
	var tasks TaskWidgets
	var err error

	ts.tasks, err = ts.listTasks(previous, next)
	if err != nil {
		return tasks, err
	}

	ts.lock.Lock()
	tasks = append(tasks, ts.tasks...)
	ts.lock.Unlock()

	sort.Sort(tasks)
	tasks.Filter()
	return tasks, nil
}

// Get a single task, by ID
func (ts *TaskSource) Get(id string) (*TaskWidget, error) {
	task, err := ts.client.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_FULL.String(),
	})
	if err != nil {
		return NewTaskWidget(&tes.Task{}), err
	}
	return NewTaskWidget(task), nil
}

func (ts *TaskSource) GetNextPage() string {
	return ts.nPage
}

func (ts *TaskSource) GetPreviousPage() string {
	if len(ts.pPage) > 0 {
		return ts.pPage[len(ts.pPage)-1]
	} else if ts.cPage != "" {
		return ts.cPage
	}
	return ""
}
