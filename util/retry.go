package util

import (
	"context"
	"math/rand"
	"time"
)

// Retrier retries failed operations until they succeed, reach MaxTries attempts,
// exceed MaxElapsedTime, or the context is canceled.
type Retrier struct {
	InitialInterval     time.Duration
	MaxInterval         time.Duration
	Multiplier          float64
	RandomizationFactor float64
	MaxElapsedTime      time.Duration
	MaxTries            int
	ShouldRetry         func(err error) bool
	Notify              func(err error, d time.Duration)
}

// NewRetrier returns a retrier configured for ten attempts over fifteen minutes.
func NewRetrier() *Retrier {
	return &Retrier{
		InitialInterval:     time.Millisecond * 500,
		MaxInterval:         time.Second * 60,
		Multiplier:          1.5,
		RandomizationFactor: 0.5,
		MaxElapsedTime:      time.Minute * 15,
		MaxTries:            10,
		ShouldRetry:         nil,
	}
}

// Retry the function f until it does not return error or the retry budget
// (MaxTries / MaxElapsedTime / context) is exhausted.
func (r *Retrier) Retry(ctx context.Context, f func() error) error {
	maxTries := r.MaxTries
	if maxTries < 1 || r.MaxElapsedTime < 0 {
		maxTries = 1
	}

	multiplier := r.Multiplier
	if multiplier < 1 {
		multiplier = 1
	}
	randFactor := r.RandomizationFactor
	if randFactor < 0 {
		randFactor = 0
	}
	maxInterval := r.MaxInterval
	if maxInterval <= 0 {
		maxInterval = time.Minute
	}

	var (
		interval = r.InitialInterval
		lastErr  error
		start    = time.Now()
	)
	for attempt := 0; attempt < maxTries; attempt++ {
		lastErr = f()
		if lastErr == nil {
			return nil
		}
		if r.ShouldRetry != nil && !r.ShouldRetry(lastErr) {
			return lastErr
		}
		if r.MaxElapsedTime > 0 && time.Since(start) > r.MaxElapsedTime {
			break
		}
		if attempt == maxTries-1 {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		sleep := time.Duration(float64(interval) * (1 + randFactor*(rand.Float64()*2-1)))
		if sleep < 0 {
			sleep = interval
		}

		if r.Notify != nil {
			r.Notify(lastErr, sleep)
		}

		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		interval = time.Duration(float64(interval) * multiplier)
		if interval > maxInterval {
			interval = maxInterval
		}
	}
	return lastErr
}
