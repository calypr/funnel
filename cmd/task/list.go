package task

import (
	"fmt"
	"io"
	"strings"

	"context"

	"github.com/ohsu-comp-bio/funnel/tes"
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

	output := &tes.ListTasksResponse{}

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

		output.Tasks = append(output.Tasks, resp.Tasks...)
		output.NextPageToken = resp.NextPageToken
		pageToken = resp.NextPageToken

		if !all || (all && pageToken == "") {
			break
		}
	}

	response := cli.Marshaler.Format(output)

	fmt.Fprintf(writer, "%s\n", response)
	return nil
}
