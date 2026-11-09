package storage

import (
	"context"
	"errors"

	"github.com/cenkalti/backoff/v7"
)

// Retrier retries operations on a storage backend using a fresh backoff per call.
type Retrier struct {
	Backend    Storage
	NewBackOff func() backoff.BackOff
	// MaxTries counts total attempts; zero still makes one attempt.
	MaxTries    uint
	ShouldRetry func(error) bool
	Notify      backoff.Notify
}

func retryStorage[T any](ctx context.Context, r *Retrier, operation func() (T, error)) (T, error) {
	options := []backoff.RetryOption{
		backoff.WithMaxTries(max(1, r.MaxTries)),
		backoff.WithMaxElapsedTime(0),
		backoff.WithNotify(r.Notify),
	}
	if r.NewBackOff != nil {
		options = append(options, backoff.WithBackOff(r.NewBackOff()))
	}
	result, err := backoff.Retry(ctx, func() (T, error) {
		result, err := operation()
		if err != nil && r.ShouldRetry != nil && !r.ShouldRetry(err) {
			return result, backoff.Permanent(err)
		}
		return result, err
	}, options...)
	if failure := backoff.AsRetryError(err); failure != nil {
		// Storage callers receive backend errors, not retry-engine metadata.
		if ctx.Err() != nil && !errors.Is(failure.Cause, backoff.ErrPermanent) && !errors.Is(failure.Cause, backoff.ErrExhausted) && !errors.Is(failure.Cause, backoff.ErrMaxElapsedTime) {
			return result, ctx.Err()
		}
		return result, failure.LastErr
	}
	return result, err
}

// Stat returns metadata about the given URL.
func (r *Retrier) Stat(ctx context.Context, url string) (*Object, error) {
	return retryStorage(ctx, r, func() (*Object, error) { return r.Backend.Stat(ctx, url) })
}

// List lists the objects at the given URL.
func (r *Retrier) List(ctx context.Context, url string) ([]*Object, error) {
	return retryStorage(ctx, r, func() ([]*Object, error) { return r.Backend.List(ctx, url) })
}

// Get copies an object from storage to a local path.
func (r *Retrier) Get(ctx context.Context, url, path string) (*Object, error) {
	return retryStorage(ctx, r, func() (*Object, error) { return r.Backend.Get(ctx, url, path) })
}

// Put copies an object from a local path to storage.
func (r *Retrier) Put(ctx context.Context, url, path string) (*Object, error) {
	return retryStorage(ctx, r, func() (*Object, error) { return r.Backend.Put(ctx, url, path) })
}

// UnsupportedOperations describes which operations are supported for the URL.
func (r *Retrier) UnsupportedOperations(url string) UnsupportedOperations {
	return r.Backend.UnsupportedOperations(url)
}

// Join joins a directory URL with the given subpath.
func (r *Retrier) Join(url, path string) (string, error) {
	return r.Backend.Join(url, path)
}
