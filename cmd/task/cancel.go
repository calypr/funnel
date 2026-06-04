package task

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ohsu-comp-bio/funnel/tes"
	"golang.org/x/net/context"
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
			jsonBytes, _ := json.Marshal(output)
			res = append(res, string(jsonBytes))
		} else {
			// Normal empty response
			out := cli.Marshaler.Format(result.Response)
			res = append(res, out)
		}
	}

	for _, x := range res {
		fmt.Fprintln(writer, x)
	}
	return nil
}
