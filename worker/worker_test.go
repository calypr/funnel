package worker

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/types/known/durationpb"
)

type pollingTaskReader struct {
	state func(context.Context) (tes.State, error)
}

func (r *pollingTaskReader) Task(context.Context) (*tes.Task, error) {
	return &tes.Task{Id: "task"}, nil
}

func (r *pollingTaskReader) State(ctx context.Context) (tes.State, error) {
	return r.state(ctx)
}

func (r *pollingTaskReader) Close() {}

func TestPollingRecordsTaskCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		worker := &DefaultWorker{
			Conf: &config.Worker{PollingRate: durationpb.New(time.Second)},
			TaskReader: &pollingTaskReader{state: func(context.Context) (tes.State, error) {
				return tes.State_CANCELED, nil
			}},
		}
		ctx, stop := worker.pollForCancel(context.Background())
		defer stop()
		<-ctx.Done()
		if !errors.Is(context.Cause(ctx), errTaskCanceled) {
			t.Fatalf("cancellation cause = %v, want task canceled", context.Cause(ctx))
		}
	})
}

func TestPollingStopWaitsForStateRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		worker := &DefaultWorker{
			Conf: &config.Worker{PollingRate: durationpb.New(time.Second)},
			TaskReader: &pollingTaskReader{state: func(ctx context.Context) (tes.State, error) {
				close(entered)
				<-ctx.Done()
				<-release
				close(finished)
				return tes.State_RUNNING, ctx.Err()
			}},
		}
		ctx, stop := worker.pollForCancel(context.Background())
		<-entered
		stopped := make(chan struct{})
		go func() {
			stop()
			close(stopped)
		}()
		synctest.Wait()
		select {
		case <-stopped:
			t.Error("polling stopped before the state read finished")
		default:
		}
		close(release)
		<-stopped
		select {
		case <-finished:
		default:
			t.Error("state read is still running")
		}
		if !errors.Is(context.Cause(ctx), context.Canceled) {
			t.Fatalf("stop cause = %v, want context canceled", context.Cause(ctx))
		}
	})
}
