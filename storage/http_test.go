package storage

import (
	"context"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestHTTP(t *testing.T) {
	store, err := NewHTTP(&config.HTTPStorage{
		Timeout: &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Duration{
				Duration: durationpb.New(time.Second * 2),
			},
		},
	})
	if err != nil {
		t.Fatal("Error creating HTTP backend:", err)
	}

	// test Get is supported
	err = store.UnsupportedOperations("https://google.com").Get
	if err != nil {
		t.Error("Unexpected error for unsupported.Get call:", err)
	}

	// test Stat is supported
	err = store.UnsupportedOperations("https://google.com").Stat
	if err != nil {
		t.Error("Unexpected error for unsupported.Stat call:", err)
	}

	// test Get succeeds
	_, err = store.Get(context.Background(), "https://google.com", "_test_download/test_https_download.html")
	if err != nil {
		t.Error("Unexpected error for Get call:", err)
	}

	// test Stat succeeds
	_, err = store.Stat(context.Background(), "https://google.com")
	if err != nil {
		t.Error("Unexpected error for Stat call:", err)
	}

	// test Get on file that requires auth; expect error
	_, err = store.Get(context.Background(), "https://s3.amazonaws.com/private", "_test_download/test_https_download_private.html")
	if err == nil {
		t.Error("Expected error for Get call")
	}

	// test client timeout
	_, err = store.Stat(context.Background(), "https://fakeurl1234.com")
	if err == nil {
		t.Error("Expected timeout error")
	}

	// test List not supported
	_, err = store.List(context.Background(), "https://google.com")
	if err == nil {
		t.Error("Expected error for List call")
	}

	// test Put not supported
	_, err = store.Put(context.Background(), "https://fakeurl1234.com", "_test_download/test_https_download.html")
	if err == nil {
		t.Error("Expected error for Put call")
	}
}
