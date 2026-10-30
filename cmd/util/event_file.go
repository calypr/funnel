package util

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
)

type eventFileWriter struct {
	*events.NDJSONWriter
	file io.Closer
	log  *logger.Logger
	once sync.Once
}

func NewEventFileWriter(path string, log *logger.Logger) (events.Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("EventFile must be set for the file event writer")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open event file: %w", err)
	}
	return &eventFileWriter{NDJSONWriter: events.NewNDJSONWriter(file), file: file, log: log}, nil
}

func (w *eventFileWriter) Close() {
	w.once.Do(func() {
		w.NDJSONWriter.Close()
		if err := w.file.Close(); err != nil {
			w.log.Error("closing event file", "error", err)
		}
	})
}
