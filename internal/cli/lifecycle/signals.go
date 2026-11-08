package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"time"
)

// SignalContext will cancel the context when any of the given
// signals is received.
func SignalContext(ctx context.Context, delay time.Duration, sigs ...os.Signal) context.Context {
	sch := make(chan os.Signal, 1)
	sub, cancel := context.WithCancel(ctx)
	signal.Notify(sch, sigs...)

	go func() {
		select {
		case <-sub.Done():
			return
		case <-sch:
			time.Sleep(delay)
			cancel()
		}
	}()

	return sub
}
