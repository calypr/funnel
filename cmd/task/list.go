package task

import (
	"fmt"
	"io"
	"strings"

	"context"

	"github.com/calypr/funnel/tes"
)

// List runs the "task list" CLI command, which connects to the server,
// calls ListTasks() and requests the given task view.
// Output is written to the given writer.
func List(server, taskView, pageToken, stateFilter string, tagsFilter []string, namePrefix string, pageSize int32, all bool, writer io.Writer) error {
	cli, err := tes.NewClient(server)
	if err != nil {
		return err
	}

	taskViewInt, err := getTaskView(taskView)
	taskView = tes.View_name[taskViewInt]
	if err != nil {
		return err
	}

	state, err := getTaskState(stateFilter)
	if err != nil {
		return err
	}

	tagKeys := []string{}
	tagVals := []string{}
	for _, v := range tagsFilter {
		parts := strings.Split(v, "=")
		if len(parts) != 2 {
			return fmt.Errorf("tags must be of the form: KEY=VALUE")
		}
		tagKeys = append(tagKeys, parts[0])
		tagVals = append(tagVals, parts[1])
	}

	started := false
	for {
		req := &tes.ListTasksRequest{
			View:       taskView,
			PageToken:  pageToken,
			PageSize:   pageSize,
			State:      state,
			TagKey:     tagKeys,
			TagValue:   tagVals,
			NamePrefix: namePrefix,
		}

		resp, err := cli.ListTasks(context.Background(), req)
		if err != nil {
			return err
		}

		if !all {
			_, err = fmt.Fprintln(writer, cli.Marshaler.Format(resp))
			return err
		}

		for _, task := range resp.Tasks {
			data, err := cli.Marshaler.Marshal(task)
			if err != nil {
				return err
			}
			separator := ",\n"
			if !started {
				separator = "{\"tasks\":[\n"
				started = true
			}
			if _, err := fmt.Fprintf(writer, "%s%s", separator, data); err != nil {
				return err
			}
		}
		pageToken = resp.NextPageToken
		if pageToken == "" {
			if started {
				_, err = fmt.Fprintln(writer, "\n]}")
			} else {
				_, err = fmt.Fprintln(writer, "{}")
			}
			return err
		}
	}
}
