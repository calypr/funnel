package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v7"
)

type retryTestStorage struct {
	Storage
	operation func(string) (*Object, error)
}

func (s *retryTestStorage) Stat(_ context.Context, url string) (*Object, error) {
	return s.operation(url)
}
func (s *retryTestStorage) Get(_ context.Context, url, _ string) (*Object, error) {
	return s.operation(url)
}
func (s *retryTestStorage) Put(_ context.Context, url, _ string) (*Object, error) {
	return s.operation(url)
}
func (s *retryTestStorage) List(_ context.Context, url string) ([]*Object, error) {
	obj, err := s.operation(url)
	if obj == nil {
		return nil, err
	}
	return []*Object{obj}, err
}
func testStorageRetrier(backend Storage, attempts int) *Retrier {
	return &Retrier{Backend: backend, MaxTries: uint(attempts), NewBackOff: func() backoff.BackOff {
		return backoff.NewConstantBackOff(time.Millisecond)
	}}
}

func TestStorageRetryAttemptsAndErrorIdentity(t *testing.T) {
	failure := errors.New("backend failed")
	for _, attempts := range []int{0, 1, 3} {
		for _, operation := range []string{"stat", "list", "get", "put"} {
			t.Run(fmt.Sprintf("%s/%d", operation, attempts), func(t *testing.T) {
				calls := 0
				r := testStorageRetrier(&retryTestStorage{operation: func(string) (*Object, error) { calls++; return nil, failure }}, attempts)
				var err error
				switch operation {
				case "stat":
					_, err = r.Stat(t.Context(), "test://item")
				case "list":
					_, err = r.List(t.Context(), "test://item")
				case "get":
					_, err = r.Get(t.Context(), "test://item", "file")
				case "put":
					_, err = r.Put(t.Context(), "test://item", "file")
				}
				if err != failure {
					t.Fatalf("error = %v, want original backend error", err)
				}
				if calls != max(1, attempts) {
					t.Fatalf("attempts = %d, want %d", calls, max(1, attempts))
				}
			})
		}
	}
}

func TestStorageRetryPermanentError(t *testing.T) {
	failure := errors.New("permanent backend failure")
	calls := 0
	r := testStorageRetrier(&retryTestStorage{operation: func(string) (*Object, error) { calls++; return nil, failure }}, 3)
	r.ShouldRetry = func(error) bool { return false }
	r.Notify = func(error, time.Duration) { t.Error("terminal error triggered retry notification") }
	_, err := r.Stat(t.Context(), "test://item")
	if err != failure || calls != 1 {
		t.Fatalf("error %v after %d attempts, want original error after one", err, calls)
	}
}

func TestStorageRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	calls := 0
	r := testStorageRetrier(&retryTestStorage{operation: func(string) (*Object, error) { calls++; return nil, errors.New("temporary") }}, 3)
	r.Notify = func(error, time.Duration) { cancel(errors.New("shutdown requested")) }
	_, err := r.Stat(ctx, "test://item")
	if err != context.Canceled || calls != 1 {
		t.Fatalf("error %v after %d attempts, want context.Canceled after one", err, calls)
	}
}

func TestStorageRetryConcurrentOperations(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	backend := &retryTestStorage{operation: func(url string) (*Object, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[url]++
		if calls[url] == 1 {
			return nil, errors.New("temporary")
		}
		return &Object{URL: url}, nil
	}}
	r := testStorageRetrier(backend, 2)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			url := fmt.Sprintf("test://%d", i)
			obj, err := r.Stat(t.Context(), url)
			if err != nil || obj == nil || obj.URL != url {
				t.Errorf("retry result %v, %v", obj, err)
			}
		})
	}
	wg.Wait()
}
