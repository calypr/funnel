package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/calypr/funnel/tes"
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
		if err != nil {
			return fmt.Errorf("opening task file %s: %w", taskFile, err)
		}
		//nolint:errcheck // task files are read-only; close is best-effort cleanup.
		defer f.Close()
		reader = io.MultiReader(reader, f)
	}

	positions := newJSONPositionReader(reader)
	dec := json.NewDecoder(positions)
	for {
		var task tes.Task
		var jsonData json.RawMessage
		err := dec.Decode(&jsonData)
		if err == io.EOF {
			break
		}
		if err != nil {
			var syntaxErr *json.SyntaxError
			switch {
			case errors.As(err, &syntaxErr):
				line, column := positions.position(syntaxErr.Offset - 1)
				return fmt.Errorf("can't load task at line %d, column %d: %w", line, column, err)
			case errors.Is(err, io.ErrUnexpectedEOF):
				line, column := positions.position(positions.size)
				return fmt.Errorf("can't load task at line %d, column %d: %w", line, column, err)
			default:
				return fmt.Errorf("can't load task: %w", err)
			}
		}
		positions.discardBefore(dec.InputOffset())
		err = protojson.Unmarshal(jsonData, &task)
		if err != nil {
			return fmt.Errorf("can't unmarshal task: %w", err)
		}
		r, err := cli.CreateTask(context.Background(), &task)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(writer, r.Id)
		if err != nil {
			return fmt.Errorf("writing created task ID: %w", err)
		}
	}

	return nil
}
