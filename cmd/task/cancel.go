package task

import (
	"encoding/json"
	"fmt"
	"io"

	"context"

	"github.com/calypr/funnel/tes"
)

// Cancel runs the "task cancel" CLI command, which connects to the server,
// calls CancelTask() on each ID, and writes output to the given writer.
func Cancel(server string, ids []string, writer io.Writer) error {
	cli, err := tes.NewClient(server)
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
			out := cli.Marshaler.Format(result.Response)
			res = append(res, out)
		}
	}

	for _, x := range res {
		if _, err := fmt.Fprintln(writer, x); err != nil {
			return fmt.Errorf("writing cancellation output: %w", err)
		}
	}
	return nil
}
