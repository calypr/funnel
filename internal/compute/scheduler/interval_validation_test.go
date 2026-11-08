package scheduler

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestSchedulerRejectsInvalidScheduleRate(t *testing.T) {
	for _, rate := range []*durationpb.Duration{nil, durationpb.New(0), durationpb.New(-1)} {
		t.Run(rate.String(), func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("scheduler panicked: %v", p)
				}
			}()
			s := &Scheduler{Conf: &config.Scheduler{ScheduleRate: rate}}
			err := s.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), "Scheduler.ScheduleRate") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
