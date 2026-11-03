package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type taskViewContextKey struct{}

type taskViewJSON struct{ data []byte }

func (j taskViewJSON) MarshalJSON() ([]byte, error) { return j.data, nil }

func withTaskView(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), taskViewContextKey{}, r.URL.Query().Get("view"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func rewriteTaskViewResponse(ctx context.Context, response proto.Message) (any, error) {
	requested, ok := ctx.Value(taskViewContextKey{}).(string)
	if !ok {
		return response, nil
	}
	switch response.(type) {
	case *tes.Task, *tes.ListTasksResponse:
	default:
		return response, nil
	}
	view, err := normalizeTaskView(requested)
	if err != nil {
		return nil, err
	}
	switch value := response.(type) {
	case *tes.Task:
		data, err := marshalTaskView(value, view)
		return taskViewJSON{data}, err
	case *tes.ListTasksResponse:
		tasks := make([]json.RawMessage, 0, len(value.Tasks))
		for _, task := range value.Tasks {
			data, err := marshalTaskView(task, view)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, data)
		}
		data, err := json.Marshal(struct {
			Tasks         []json.RawMessage `json:"tasks"`
			NextPageToken string            `json:"next_page_token,omitempty"`
		}{Tasks: tasks, NextPageToken: value.NextPageToken})
		return taskViewJSON{data}, err
	default:
		return response, nil
	}
}

func marshalTaskView(task *tes.Task, view string) ([]byte, error) {
	switch view {
	case tes.View_MINIMAL.String():
		return json.Marshal(struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}{ID: task.Id, State: task.State.String()})
	case tes.View_BASIC.String():
		return marshalBasicTaskView(task)
	default:
		return (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(task)
	}
}

func marshalBasicTaskView(task *tes.Task) ([]byte, error) {
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(task.GetBasicView())
	if err != nil {
		return nil, err
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if inputs, ok := result["inputs"].([]any); ok {
		for _, input := range inputs {
			if fields, ok := input.(map[string]any); ok {
				delete(fields, "content")
			}
		}
	}
	if logs, ok := result["logs"].([]any); ok {
		for _, entry := range logs {
			if fields, ok := entry.(map[string]any); ok {
				delete(fields, "system_logs")
				if executors, ok := fields["logs"].([]any); ok {
					for _, executor := range executors {
						if fields, ok := executor.(map[string]any); ok {
							delete(fields, "stdout")
							delete(fields, "stderr")
						}
					}
				}
			}
		}
	}
	return json.Marshal(result)
}
