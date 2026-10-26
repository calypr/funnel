package util

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRetrier(t *testing.T) {
	r := &Retrier{
		MaxTries:            3,
		ShouldRetry:         nil,
		InitialInterval:     time.Millisecond * 10,
		MaxInterval:         time.Second * 60,
		Multiplier:          2.0,
		MaxElapsedTime:      0,
		RandomizationFactor: 0,
	}
	bg := context.Background()

	var delays []time.Duration
	r.Notify = func(err error, d time.Duration) {
		delays = append(delays, d)
	}

	i := 0
	r.Retry(bg, func() error {
		i++
		return fmt.Errorf("always error")
	})
	if i != 3 {
		t.Error("unexpected number of retries", i)
	}
	// backoff starts at InitialInterval and doubles each attempt
	if len(delays) != 2 || delays[0] != time.Millisecond*10 || delays[1] != time.Millisecond*20 {
		t.Error("unexpected backoff delays", delays)
	}

	r.Retry(bg, func() error {
		return nil
	})
	// a successful retry resets the interval
	if len(delays) != 2 {
		t.Error("expected no notification on success", delays)
	}
}

func TestRetrierMaxTriesAtLeastOneAttempt(t *testing.T) {
	for _, maxTries := range []int{0, -1} {
		t.Run(fmt.Sprint(maxTries), func(t *testing.T) {
			wantErr := fmt.Errorf("always error")
			r := &Retrier{
				MaxTries:            maxTries,
				InitialInterval:     time.Millisecond,
				MaxInterval:         time.Second,
				Multiplier:          2,
				MaxElapsedTime:      0,
				RandomizationFactor: 0,
			}

			attempts := 0
			gotErr := r.Retry(context.Background(), func() error {
				attempts++
				return wantErr
			})

			if attempts != 1 {
				t.Fatalf("got %d attempts, want 1", attempts)
			}
			if gotErr != wantErr {
				t.Fatalf("got error %v, want original error %v", gotErr, wantErr)
			}
		})
	}
}

func TestRetrierNegativeMaxElapsedTimeStopsAfterOneAttempt(t *testing.T) {
	r := &Retrier{
		MaxTries:            3,
		InitialInterval:     time.Millisecond,
		MaxInterval:         time.Second,
		Multiplier:          2,
		MaxElapsedTime:      -1,
		RandomizationFactor: 0,
	}

	attempts := 0
	gotErr := r.Retry(context.Background(), func() error {
		attempts++
		return fmt.Errorf("always error")
	})

	if attempts != 1 {
		t.Fatalf("got %d attempts, want 1", attempts)
	}
	if gotErr == nil {
		t.Fatal("got nil error, want operation error")
	}
}

func TestRetrierShouldRetryAndNotify(t *testing.T) {
	wantErr := fmt.Errorf("temporary error")
	r := &Retrier{
		MaxTries:            3,
		InitialInterval:     time.Millisecond,
		MaxInterval:         time.Second,
		Multiplier:          2,
		MaxElapsedTime:      0,
		RandomizationFactor: 0,
	}

	var attempts, notifications int
	r.Notify = func(err error, delay time.Duration) {
		if err != wantErr {
			t.Errorf("notified with error %v, want %v", err, wantErr)
		}
		if delay != time.Millisecond {
			t.Errorf("notified with delay %v, want %v", delay, time.Millisecond)
		}
		notifications++
	}
	r.ShouldRetry = func(error) bool {
		return attempts == 1
	}

	gotErr := r.Retry(context.Background(), func() error {
		attempts++
		return wantErr
	})

	if attempts != 2 {
		t.Fatalf("got %d attempts, want 2", attempts)
	}
	if notifications != 1 {
		t.Fatalf("got %d notifications, want 1", notifications)
	}
	if gotErr != wantErr {
		t.Fatalf("got error %v, want original error %v", gotErr, wantErr)
	}
}

func TestRetrierCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Retrier{
		MaxTries:            3,
		InitialInterval:     time.Hour,
		MaxInterval:         time.Hour,
		Multiplier:          2,
		MaxElapsedTime:      0,
		RandomizationFactor: 0,
	}
	r.Notify = func(error, time.Duration) {
		cancel()
	}

	attempts := 0
	gotErr := r.Retry(ctx, func() error {
		attempts++
		return fmt.Errorf("temporary error")
	})

	if attempts != 1 {
		t.Fatalf("got %d attempts, want 1", attempts)
	}
	if gotErr != context.Canceled {
		t.Fatalf("got error %v, want %v", gotErr, context.Canceled)
	}
}
