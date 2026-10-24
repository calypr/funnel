package worker

import (
	"io"

	"github.com/calypr/funnel/events"
)

type Command struct {
	Image        string
	ShellCommand []string
	Volumes      []Volume
	Workdir      string
	Env          map[string]string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Event        *events.ExecutorWriter
	TaskCommand
}

func (c *Command) GetStdout() io.Writer {
	return c.Stdout
}

func (c *Command) SetStdout(w io.Writer) {
	c.Stdout = w
}

func (c *Command) GetStderr() io.Writer {
	return c.Stderr
}

func (c *Command) SetStderr(w io.Writer) {
	c.Stderr = w
}

func (c *Command) SetStdin(r io.Reader) {
	c.Stdin = r
}
