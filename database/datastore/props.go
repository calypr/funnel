package datastore

import (
	"context"
	"fmt"
	"net/url"

	"cloud.google.com/go/datastore"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
)

/*
Entity group and key structure:

"Task" holds the basic task view.

"TaskPart" holds the various parts of the full view:
stdout, stderr, and input content.
It has an parent link to the "Task".
*/

func taskKey(id string) *datastore.Key {
	return datastore.NameKey("Task", id, nil)
}

func sysLogsKey(id string, attempt uint32) *datastore.Key {
	k := fmt.Sprintf("syslogs-%d", attempt)
	return datastore.NameKey("TaskPart", k, taskKey(id))
}

func contentKey(id string, index int) *datastore.Key {
	k := fmt.Sprintf("input-content-%d", index)
	return datastore.NameKey("TaskPart", k, taskKey(id))
}

func stdoutKey(id string, attempt, index uint32) *datastore.Key {
	k := fmt.Sprintf("stdout-%d-%d", attempt, index)
	return datastore.NameKey("TaskPart", k, taskKey(id))
}

func stderrKey(id string, attempt, index uint32) *datastore.Key {
	k := fmt.Sprintf("stderr-%d-%d", attempt, index)
	return datastore.NameKey("TaskPart", k, taskKey(id))
}

func viewPartKeys(t *tes.Task) []*datastore.Key {
	var parts []*datastore.Key
	for i := range t.Inputs {
		parts = append(parts, contentKey(t.Id, i))
	}
	for attempt, a := range t.Logs {
		parts = append(parts, sysLogsKey(t.Id, uint32(attempt)))
		for index := range a.Logs {
			parts = append(parts, stdoutKey(t.Id, uint32(attempt), uint32(index)))
			parts = append(parts, stderrKey(t.Id, uint32(attempt), uint32(index)))
		}
	}
	return parts
}

/*
Datastore is missing support for some types we need:
- map[string]string
- uint32

So we need to do some extra work to map a task to/from a []datastore.Property.
It also allows us to be very selective about what gets saved and indexed.
*/

type partType int

const (
	contentPart partType = iota
	stdoutPart
	stderrPart
	sysLogsPart
)

type part struct {
	Type partType `datastore:",noindex"`
	// Index is used for both input content and executor stdout/err
	Attempt, Index int      `datastore:",noindex,omitempty"`
	Stdout, Stderr string   `datastore:",noindex,omitempty"`
	Content        string   `datastore:",noindex,omitempty"`
	SystemLogs     []string `datastore:",noindex,omitempty"`
}

type task struct {
	Id, CreationTime  string `datastore:",omitempty"` // nolint
	State             int32
	Owner             string
	Name, Description string     `datastore:",noindex,omitempty"`
	Executors         []executor `datastore:",noindex,omitempty"`
	Inputs            []param    `datastore:",noindex,omitempty"`
	Outputs           []param    `datastore:",noindex,omitempty"`
	Volumes           []string   `datastore:",noindex,omitempty"`
	Tags              []kv       `datastore:",noindex,omitempty"`
	TagStrings        []string
	Resources         *resources `datastore:",noindex,omitempty"`
	TaskLogs          []tasklog  `datastore:",noindex,omitempty"`
}

type tasklog struct {
	Metadata []kv `datastore:",noindex,omitempty"`
	*tes.TaskLog
}

type resources struct {
	CpuCores      int32    `datastore:",noindex,omitempty"` // nolint
	RamGb, DiskGb float64  `datastore:",noindex,omitempty"` // nolint
	Preemptible   bool     `datastore:",noindex,omitempty"`
	Zones         []string `datastore:",noindex,omitempty"`
}

type executor struct {
	Image, Workdir, Stdin, Stdout, Stderr string   `datastore:",noindex,omitempty"`
	Command                               []string `datastore:",noindex,omitempty"`
	Env                                   []kv     `datastore:",noindex,omitempty"`
}

type param struct {
	Name, Description, Url, Path, Content string `datastore:",noindex,omitempty"` // nolint
	Type                                  int32  `datastore:",noindex,omitempty"`
}

func marshalTask(t *tes.Task, ctx context.Context) ([]*datastore.Key, []interface{}) {
	z := &task{
		Id:           t.Id,
		State:        int32(t.State),
		Owner:        server.GetUsername(ctx),
		CreationTime: t.CreationTime,
		Name:         t.Name,
		Description:  t.Description,
		Volumes:      t.Volumes,
		Tags:         marshalMap(t.Tags),
		TagStrings:   stringifyMap(t.Tags),
	}
	if t.Resources != nil {
		z.Resources = &resources{
			CpuCores:    t.Resources.CpuCores,
			RamGb:       t.Resources.RamGb,
			DiskGb:      t.Resources.DiskGb,
			Preemptible: t.Resources.Preemptible,
			Zones:       t.Resources.Zones,
		}
	}
	for _, e := range t.Executors {
		z.Executors = append(z.Executors, executor{
			Image:   e.Image,
			Workdir: e.Workdir,
			Stdin:   e.Stdin,
			Stdout:  e.Stdout,
			Stderr:  e.Stderr,
			Command: e.Command,
			Env:     marshalMap(e.Env),
		})
	}
	for _, i := range t.Inputs {
		z.Inputs = append(z.Inputs, param{
			Name:        i.Name,
			Description: i.Description,
			Url:         i.Url,
			Path:        i.Path,
			Type:        int32(i.Type),
		})
	}
	for _, i := range t.Outputs {
		z.Outputs = append(z.Outputs, param{
			Name:        i.Name,
			Description: i.Description,
			Url:         i.Url,
			Path:        i.Path,
			Type:        int32(i.Type),
		})
	}
	for _, i := range t.Logs {
		z.TaskLogs = append(z.TaskLogs, tasklog{
			TaskLog:  i,
			Metadata: marshalMap(i.Metadata),
		})
	}

	keys := []*datastore.Key{
		taskKey(t.Id),
	}
	data := []interface{}{
		z,
	}
	for i, input := range t.Inputs {
		if input.Content != "" {
			keys = append(keys, contentKey(t.Id, i))
			data = append(data, &part{
				Index:   i,
				Content: input.Content,
			})
		}
	}

	return keys, data
}

func (c *task) unmarshal() *tes.Task {
	z := &tes.Task{
		Id:           c.Id,
		CreationTime: c.CreationTime,
		State:        tes.State(c.State),
		Name:         c.Name,
		Description:  c.Description,
		Volumes:      c.Volumes,
		Tags:         unmarshalMap(c.Tags),
	}

	if c.Resources != nil {
		z.Resources = &tes.Resources{
			CpuCores:    c.Resources.CpuCores,
			RamGb:       c.Resources.RamGb,
			DiskGb:      c.Resources.DiskGb,
			Preemptible: c.Resources.Preemptible,
			Zones:       c.Resources.Zones,
		}
	}
	for _, e := range c.Executors {
		z.Executors = append(z.Executors, &tes.Executor{
			Image:   e.Image,
			Workdir: e.Workdir,
			Stdin:   e.Stdin,
			Stdout:  e.Stdout,
			Stderr:  e.Stderr,
			Command: e.Command,
			Env:     unmarshalMap(e.Env),
		})
	}
	for _, i := range c.Inputs {
		z.Inputs = append(z.Inputs, &tes.Input{
			Name:        i.Name,
			Description: i.Description,
			Url:         i.Url,
			Path:        i.Path,
			Type:        tes.FileType(i.Type),
		})
	}
	for _, i := range c.Outputs {
		z.Outputs = append(z.Outputs, &tes.Output{
			Name:        i.Name,
			Description: i.Description,
			Url:         i.Url,
			Path:        i.Path,
			Type:        tes.FileType(i.Type),
		})
	}
	for _, i := range c.TaskLogs {
		tl := i.TaskLog
		tl.Metadata = unmarshalMap(i.Metadata)
		z.Logs = append(z.Logs, tl)
	}
	// Ensure at least one empty log for the JSON response:
	if len(z.Logs) == 0 {
		z.Logs = []*tes.TaskLog{{}}
	}
	return z
}

func unmarshalPart(t *tes.Task, props datastore.PropertyList) error {
	e := &part{}
	err := datastore.LoadStruct(e, props)
	if err != nil {
		return err
	}
	switch e.Type {
	case contentPart:
		t.Inputs[e.Index].Content = e.Content
	case sysLogsPart:
		t.GetTaskLog(e.Attempt).SystemLogs = e.SystemLogs
	case stdoutPart:
		t.GetExecLog(e.Attempt, e.Index).Stdout = e.Stdout
	case stderrPart:
		t.GetExecLog(e.Attempt, e.Index).Stderr = e.Stderr
	}
	return nil
}

func marshalEvent(e *events.Event) *part {
	z := &part{
		Attempt: int(e.Attempt),
		Index:   int(e.Index),
	}
	switch e.Type {
	case events.Type_EXECUTOR_STDOUT:
		z.Type = stdoutPart
		z.Stdout = e.GetStdout()
	case events.Type_EXECUTOR_STDERR:
		z.Type = stderrPart
		z.Stderr = e.GetStderr()
	}
	return z
}

type kv struct {
	Key, Value string
}

func marshalMap(m map[string]string) []kv {
	var out []kv
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	return out
}

func unmarshalMap(kvs []kv) map[string]string {
	out := map[string]string{}
	for _, kv := range kvs {
		out[kv.Key] = kv.Value
	}
	return out
}

func stringifyMap(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, encodeKV(k, v))
	}
	return out
}

func encodeKV(k, v string) string {
	return url.QueryEscape(k) + ":" + url.QueryEscape(v)
}

func mergeKvs(existing []kv, updates map[string]string) []kv {
	tmp := map[string]string{}

	// Populate 'tmp' with existing values:
	for _, kv := range existing {
		tmp[kv.Key] = kv.Value
	}

	// Update 'tmp' with new values:
	for k, v := range updates {
		tmp[k] = v
	}

	// Convert 'tmp' to a kv-list:
	result := []kv{}
	for k, v := range tmp {
		result = append(result, kv{Key: k, Value: v})
	}
	return result
}
