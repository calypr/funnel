package task

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ohsu-comp-bio/funnel/tes"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/encoding/protojson"
)

// Create runs the "task create" CLI command, connecting to the server,
// calling CreateTask, and writing output to the given writer.
// Tasks are loaded from the "files" arg. "files" are file paths to JSON objects.
func Create(server string, files []string, reader io.Reader, writer io.Writer) error {
	cli, err := tes.NewClient(server)
	if err != nil {
		return err
	}

	for _, taskFile := range files {
		f, err := os.Open(taskFile)
		defer f.Close()
		if err != nil {
			return err
		}
		reader = io.MultiReader(reader, f)
	}

	dec := json.NewDecoder(reader)
	for {
		var task tes.Task
		var jsonData json.RawMessage
		err := dec.Decode(&jsonData)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("can't load task: %s", err)
		}
		err = protojson.Unmarshal(jsonData, &task)
		if err != nil {
			return fmt.Errorf("can't unmarshal task: %s", err)
		}
		r, err := cli.CreateTask(context.Background(), &task)
		if err != nil {
			return err
		}
		fmt.Fprintln(writer, r.Id)
	}

	return nil
}
