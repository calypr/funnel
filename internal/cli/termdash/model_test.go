package termdash

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/tes"
	tea "github.com/charmbracelet/bubbletea"
)

type fakeTaskClient struct {
	listTasks func(*tes.ListTasksRequest) (*tes.ListTasksResponse, error)
}

func (c *fakeTaskClient) ListTasks(_ context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return c.listTasks(req)
}

func (*fakeTaskClient) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return &tes.Task{}, nil
}

func (*fakeTaskClient) CancelTask(context.Context, *tes.CancelTaskRequest) (*tes.CancelTaskResponse, error) {
	return &tes.CancelTaskResponse{}, nil
}

func TestListQueuePreservesNavigationAndCoalescesRefresh(t *testing.T) {
	requestTokens := make(chan string, 4)
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	client := &fakeTaskClient{
		listTasks: func(req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
			requestTokens <- req.PageToken
			switch req.PageToken {
			case "":
				<-releaseFirst
				return &tes.ListTasksResponse{
					Tasks:         []*tes.Task{{Id: "page1"}},
					NextPageToken: "token2",
				}, nil
			case "token2":
				return &tes.ListTasksResponse{
					Tasks:         []*tes.Task{{Id: "page2"}},
					NextPageToken: "token3",
				}, nil
			case "token3":
				return &tes.ListTasksResponse{
					Tasks:         []*tes.Task{{Id: "page3"}},
					NextPageToken: "token4",
				}, nil
			default:
				return nil, errors.New("unexpected page token")
			}
		},
	}
	source := &TaskSource{client: client, pageSize: 100, nPage: "token2"}
	d := &dashboard{source: source, mode: modeList, sortField: "id"}

	firstResult := make(chan tea.Msg, 1)
	firstCmd := d.requestList(listCurrent)
	go func() { firstResult <- firstCmd() }()
	if got := receiveToken(t, requestTokens); got != "" {
		t.Fatalf("first request token = %q, want current page", got)
	}

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd != nil {
		t.Fatal("navigation started a second list request while one was in flight")
	}
	_, cmd = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd != nil {
		t.Fatal("second navigation started a list request while one was in flight")
	}
	d.Update(tickMsg(time.Now()))
	d.Update(tickMsg(time.Now()))
	if got := len(requestTokens); got != 0 {
		t.Fatalf("%d additional list requests started before the first completed", got)
	}

	release()
	_, cmd = d.Update(receiveMessage(t, firstResult))
	msg := runListCmd(t, cmd)
	if got := receiveToken(t, requestTokens); got != "token2" {
		t.Fatalf("first navigation token = %q, want token2", got)
	}
	_, cmd = d.Update(msg)
	msg = runListCmd(t, cmd)
	if got := receiveToken(t, requestTokens); got != "token3" {
		t.Fatalf("second navigation token = %q, want token3", got)
	}
	_, cmd = d.Update(msg)
	msg = runListCmd(t, cmd)
	if got := receiveToken(t, requestTokens); got != "token3" {
		t.Fatalf("coalesced refresh token = %q, want current page token3", got)
	}
	_, cmd = d.Update(msg)
	if cmd != nil {
		t.Fatal("unexpected list request remained after the coalesced refresh")
	}
	if got := d.tasks[0].Id; got != "page3" {
		t.Fatalf("displayed page = %q, want page3", got)
	}
	if got := source.NextPageToken(); got != "token4" {
		t.Fatalf("next-page token = %q, want token4", got)
	}
}

func TestFailedNavigationPreservesPageAndTokens(t *testing.T) {
	requestTokens := make(chan string, 3)
	client := &fakeTaskClient{
		listTasks: func(req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
			requestTokens <- req.PageToken
			switch req.PageToken {
			case "":
				return &tes.ListTasksResponse{
					Tasks:         []*tes.Task{{Id: "page1"}},
					NextPageToken: "token2",
				}, nil
			case "token2":
				return &tes.ListTasksResponse{
					Tasks:         []*tes.Task{{Id: "page2"}},
					NextPageToken: "token3",
				}, nil
			case "token3":
				return nil, errors.New("page request failed")
			default:
				return nil, errors.New("unexpected page token")
			}
		},
	}
	source := &TaskSource{client: client, pageSize: 100}
	page1, err := source.List(false, false, "id", false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := page1[0].Id; got != "page1" {
		t.Fatalf("first page = %q, want page1", got)
	}
	if got := receiveToken(t, requestTokens); got != "" {
		t.Fatalf("first request token = %q, want first page", got)
	}
	page2, err := source.List(false, true, "id", false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := receiveToken(t, requestTokens); got != "token2" {
		t.Fatalf("second request token = %q, want token2", got)
	}

	d := &dashboard{
		source:    source,
		mode:      modeList,
		tasks:     page2,
		selected:  "page2",
		sortField: "id",
	}
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd == nil {
		t.Fatal("next-page request was not started")
	}
	_, cmd = d.Update(cmd())
	if cmd != nil {
		t.Fatal("unexpected queued list request")
	}

	if got := receiveToken(t, requestTokens); got != "token3" {
		t.Fatalf("failed request token = %q, want token3", got)
	}
	if got := d.tasks[0].Id; got != "page2" {
		t.Fatalf("displayed page after failure = %q, want page2", got)
	}
	if got := source.PreviousPageToken(); got != "token2" {
		t.Fatalf("previous-page token after failure = %q, want token2", got)
	}
	if got := source.NextPageToken(); got != "token3" {
		t.Fatalf("next-page token after failure = %q, want token3", got)
	}
	if got := source.cPage; got != "token2" {
		t.Fatalf("current-page token after failure = %q, want token2", got)
	}
	if len(source.pPage) != 0 {
		t.Fatalf("previous-page stack after failure = %v, want empty", source.pPage)
	}
	if d.err != "page request failed" {
		t.Fatalf("dashboard error = %q, want page request failure", d.err)
	}
}

func TestListQueuePreservesReversalsAtPageBoundaries(t *testing.T) {
	for _, lastPage := range []bool{false, true} {
		name := "first page"
		if lastPage {
			name = "last page"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			releaseFirst := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
			defer release()
			var blockFirst atomic.Bool
			blockFirst.Store(true)
			tokens := make(chan string, 2)
			source := &TaskSource{pageSize: 100, nPage: "token2"}
			first, reverse, initialTask := "n", "p", "page1"
			if lastPage {
				source.cPage, source.nPage = "token2", ""
				first, reverse, initialTask = "p", "n", "page2"
			}
			source.client = &fakeTaskClient{listTasks: func(req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
				tokens <- req.PageToken
				if blockFirst.Swap(false) {
					close(started)
					<-releaseFirst
				}
				if req.PageToken == "" {
					return &tes.ListTasksResponse{Tasks: []*tes.Task{{Id: "page1"}}, NextPageToken: "token2"}, nil
				}
				return &tes.ListTasksResponse{Tasks: []*tes.Task{{Id: "page2"}}}, nil
			}}
			d := &dashboard{source: source, mode: modeList, sortField: "id", tasks: []*tes.Task{{Id: initialTask}}}
			_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(first)})
			if cmd == nil {
				t.Fatal("initial navigation did not start")
			}
			result := make(chan tea.Msg, 1)
			go func() { result <- cmd() }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("initial navigation did not reach the server")
			}
			_, reverseCmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(reverse)})
			if reverseCmd != nil {
				t.Fatal("reversal started concurrently with the first navigation")
			}
			release()
			_, cmd = d.Update(receiveMessage(t, result))
			if cmd == nil {
				t.Fatal("queued reversal was discarded at the page boundary")
			}
			_, cmd = d.Update(cmd())
			if cmd != nil {
				t.Fatal("unexpected request after reversing navigation")
			}
			if d.tasks[0].Id != initialTask {
				t.Fatalf("displayed task = %q, want original %q", d.tasks[0].Id, initialTask)
			}
			firstToken, reverseToken := receiveToken(t, tokens), receiveToken(t, tokens)
			if firstToken == reverseToken {
				t.Fatalf("navigation did not reverse page tokens: %q, %q", firstToken, reverseToken)
			}
		})
	}
}

func receiveToken(t *testing.T, tokens <-chan string) string {
	t.Helper()
	select {
	case token := <-tokens:
		return token
	case <-time.After(3 * time.Second):
		t.Fatal("list request did not start")
		return ""
	}
}

func receiveMessage(t *testing.T, messages <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("list request did not finish")
		return nil
	}
}

func runListCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a queued list command")
		return nil
	}
	return cmd()
}
