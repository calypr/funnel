package storage

import (
	"net/url"
	"testing"
)

func TestFTPJoinEscapesPathSegments(t *testing.T) {
	tests := []struct {
		name, base, path, wantURL, wantPath string
	}{
		{
			name:     "fragment character",
			base:     "ftp://user:pass@example.com/upload/dir",
			path:     "result#1.txt",
			wantURL:  "ftp://user:pass@example.com/upload/dir/result%231.txt",
			wantPath: "/upload/dir/result#1.txt",
		},
		{
			name:     "query character",
			base:     "ftp://user:pass@example.com/upload/dir",
			path:     "a?b.txt",
			wantURL:  "ftp://user:pass@example.com/upload/dir/a%3Fb.txt",
			wantPath: "/upload/dir/a?b.txt",
		},
		{
			name:     "encoded percent sequence",
			base:     "ftp://user:pass@example.com/upload/dir",
			path:     "50%25.txt",
			wantURL:  "ftp://user:pass@example.com/upload/dir/50%2525.txt",
			wantPath: "/upload/dir/50%25.txt",
		},
		{
			name:     "literal percent",
			base:     "ftp://user:pass@example.com/upload/dir",
			path:     "100%.txt",
			wantURL:  "ftp://user:pass@example.com/upload/dir/100%25.txt",
			wantPath: "/upload/dir/100%.txt",
		},
		{
			name:     "space",
			base:     "ftp://user:pass@example.com/upload/dir",
			path:     "a b.txt",
			wantURL:  "ftp://user:pass@example.com/upload/dir/a%20b.txt",
			wantPath: "/upload/dir/a b.txt",
		},
		{
			name:     "nested path",
			base:     "ftp://user:pass@example.com/upload/dir",
			path:     "sub/a?b.txt",
			wantURL:  "ftp://user:pass@example.com/upload/dir/sub/a%3Fb.txt",
			wantPath: "/upload/dir/sub/a?b.txt",
		},
		{
			name:     "escaped base and query",
			base:     "ftp://user:pass@example.com/base%20dir/folder%2525?token=a%2Bb",
			path:     "new dir/a#b.txt",
			wantURL:  "ftp://user:pass@example.com/base%20dir/folder%2525/new%20dir/a%23b.txt?token=a%2Bb",
			wantPath: "/base dir/folder%25/new dir/a#b.txt",
		},
	}

	store := &FTP{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Join(tt.base, tt.path)
			if err != nil {
				t.Fatalf("Join(%q, %q) returned error: %v", tt.base, tt.path, err)
			}
			if got != tt.wantURL {
				t.Errorf("Join(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.wantURL)
			}

			joined, err := url.Parse(got)
			if err != nil {
				t.Fatalf("joined URL %q is invalid: %v", got, err)
			}
			if joined.Path != tt.wantPath {
				t.Errorf("joined URL path = %q, want %q", joined.Path, tt.wantPath)
			}
		})
	}
}

func TestFTPJoinRejectsMalformedBaseURL(t *testing.T) {
	if _, err := (&FTP{}).Join("ftp://[::1", "file.txt"); err == nil {
		t.Fatal("Join accepted a malformed base URL")
	}
}
