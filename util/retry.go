package util

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v7"
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
	backoff             backoff.BackOff
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

// Retry the function f until it does not return error or BackOff stops.
func (r *Retrier) Retry(ctx context.Context, f func() error) error {
	maxTries := r.MaxTries
	if maxTries < 1 || r.MaxElapsedTime < 0 {
		maxTries = 1
	}

	r.backoff = &backoff.ExponentialBackOff{
		InitialInterval:     r.InitialInterval,
		MaxInterval:         r.MaxInterval,
		Multiplier:          r.Multiplier,
		RandomizationFactor: r.RandomizationFactor,
	}

	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		return struct{}{}, r.checkErr(f())
	},
		backoff.WithBackOff(r.backoff),
		backoff.WithMaxTries(uint(maxTries)),
		backoff.WithMaxElapsedTime(r.MaxElapsedTime),
		backoff.WithNotify(r.notify),
	)
	if err == nil {
		return nil
	}

	retryErr := backoff.AsRetryError(err)
	if retryErr == nil {
		return err
	}
	if errors.Is(retryErr.Cause, backoff.ErrPermanent) {
		return retryErr.LastErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return retryErr.LastErr
}

func (r *Retrier) notify(err error, d time.Duration) {
	if r.Notify != nil {
		r.Notify(err, d)
	}
}

func (r *Retrier) checkErr(err error) error {
	switch {
	case err != nil && r.ShouldRetry != nil && !r.ShouldRetry(err):
		return backoff.Permanent(err)
	case err != nil:
		return err
	default:
		return nil
	}
}
