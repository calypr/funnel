package storage

import (
	"context"

	"sync"

	"github.com/calypr/funnel/util/fsutil"
)

// Transfer defines the interface of a single storage upload or download request.
//
// Transfer events (started, failed, finished, etc) are communicated
// via the Transfer interface.
type Transfer interface {
	URL() string
	Path() string
	Started()
	Finished(obj *Object)
	Failed(err error)
}

// Download downloads a list of transfers from storage, in parallel.
//
// Transfer events (started, failed, finished, etc) are communicated
// via the Transfer interface.
func Download(ctx context.Context, store Storage, transfers []Transfer, parallelLimit int) {
	if parallelLimit < 1 {
		parallelLimit = 1
	}
	sem := make(chan struct{}, parallelLimit)
	var wg sync.WaitGroup
	for _, x := range transfers {
		x := x
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			x.Started()
			var obj *Object
			err := fsutil.EnsurePath(x.Path())
			if err == nil {
				obj, err = store.Get(ctx, x.URL(), x.Path())
			}
			if err != nil {
				x.Failed(err)
			} else {
				x.Finished(obj)
			}
		}()
	}
	wg.Wait()
}

// Upload uploads a list of transfers to storage, in parallel.
//
// Transfer events (started, failed, finished, etc) are communicated
// via the Transfer interface.
func Upload(ctx context.Context, store Storage, transfers []Transfer, parallelLimit int) {
	if parallelLimit < 1 {
		parallelLimit = 1
	}
	sem := make(chan struct{}, parallelLimit)
	var wg sync.WaitGroup
	for _, x := range transfers {
		x := x
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			x.Started()
			obj, err := store.Put(ctx, x.URL(), x.Path())
			if err != nil {
				x.Failed(err)
			} else {
				x.Finished(obj)
			}
		}()
	}
	wg.Wait()
}
