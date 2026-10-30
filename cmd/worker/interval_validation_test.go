package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWorkerRejectsInvalidPollingRate(t *testing.T) {
	for _, rate := range []*durationpb.Duration{nil, durationpb.New(0), durationpb.New(-1)} {
		conf := config.DefaultConfig()
		conf.Worker.PollingRate = rate
		_, err := NewWorker(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()), &Options{})
		if err == nil || !strings.Contains(err.Error(), "Worker.PollingRate") {
			t.Errorf("rate %v: error = %v", rate, err)
		}
	}
}
