package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/cli/tesenv"
)

// Cancel runs the "task cancel" CLI command, which connects to the server,
// calls CancelTask() on each ID, and writes output to the given writer.
func Cancel(server string, ids []string, writer io.Writer) error {
	cli, err := tesenv.New(server)
	if err != nil {
		return err
	}

	res := []string{}

	for _, taskID := range ids {
		result, err := cli.CancelTaskWithMessage(context.Background(), &tes.CancelTaskRequest{Id: taskID})
		if err != nil {
			return err
		}

		// If there's an informational message, print it as JSON
		if result.Message != "" {
			output := map[string]interface{}{
				"message": result.Message,
			}
			jsonBytes, err := json.Marshal(output)
			if err != nil {
				return fmt.Errorf("marshaling cancellation message: %w", err)
			}
			res = append(res, string(jsonBytes))
		} else {
			// Normal empty response
			out := (protojson.MarshalOptions{Indent: "  "}).Format(result.Response)
			res = append(res, out)
		}
	}

	for _, x := range res {
		_, err := fmt.Fprintln(writer, x)
		if err != nil {
			return fmt.Errorf("writing cancellation output: %w", err)
		}
	}
	return nil
}
