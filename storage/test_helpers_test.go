package storage

import (
	"io"
	"testing"
)

func writeTestResponse(t testing.TB, w io.Writer, body []byte) {
	t.Helper()
	_, err := w.Write(body)
	if err != nil {
		t.Errorf("write test response: %v", err)
	}
}

func closeTestResource(t testing.TB, resource io.Closer) {
	t.Helper()
	t.Cleanup(func() {
		err := resource.Close()
		if err != nil {
			t.Errorf("close test resource: %v", err)
		}
	})
}
