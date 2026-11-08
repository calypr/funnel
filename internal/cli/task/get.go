package task

import (
	"context"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/cli/tesenv"
)

// Get runs the "task get" CLI command, which connects to the server,
// calls GetTask for each ID, requesting the given task view, and writes
// output to the given writer.
func Get(server string, ids []string, taskView string, w io.Writer) error {
	cli, err := tesenv.New(server)
	if err != nil {
		return err
	}

	res := []string{}
	marshaler := protojson.MarshalOptions{Indent: "  "}
	marshaler.EmitDefaultValues = true

	taskValue, err := getTaskView(taskView)
	taskView = tes.View_name[taskValue]
	if err != nil {
		return err
	}

	for _, taskID := range ids {
		resp, err := cli.GetTask(context.Background(), &tes.GetTaskRequest{
			Id:   taskID,
			View: taskView,
		})
		if err != nil {
			return err
		}
		out := marshaler.Format(resp)
		res = append(res, out)
	}

	for _, x := range res {
		_, err := fmt.Fprintln(w, x)
		if err != nil {
			return fmt.Errorf("writing task output: %w", err)
		}
	}
	return nil
}
