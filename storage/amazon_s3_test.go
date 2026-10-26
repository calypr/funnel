package storage

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
)

func TestAmazonS3Transfers(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	keyFile := filepath.Join(t.TempDir(), "sse-key")
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum(key)
	for _, test := range []struct {
		name string
		sse  *config.SSE
	}{
		{name: "plain"},
		{name: "customer key", sse: &config.SSE{CustomerKeyFile: keyFile}},
		{name: "kms key", sse: &config.SSE{KMSKey: "test-kms-key"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var uploaded []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bucket/dir/object" {
					t.Errorf("path = %q", r.URL.Path)
				}
				if !strings.Contains(r.Header.Get("Authorization"), "/eu-west-1/s3/") {
					t.Error("request did not use configured region and credentials")
				}
				if test.sse != nil && test.sse.CustomerKeyFile != "" {
					if r.Header.Get("X-Amz-Server-Side-Encryption-Customer-Key") != base64.StdEncoding.EncodeToString(key) {
						t.Error("incorrect SSE-C key encoding")
					}
					if r.Header.Get("X-Amz-Server-Side-Encryption-Customer-Key-Md5") != base64.StdEncoding.EncodeToString(digest[:]) {
						t.Error("incorrect SSE-C key digest")
					}
				}
				w.Header().Set("ETag", `"fixture"`)
				w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
				switch r.Method {
				case http.MethodPut:
					if r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" || r.Header.Get("Content-Encoding") == "aws-chunked" {
						t.Error("unexpected optional checksum on compatible endpoint")
					}
					if test.sse != nil && test.sse.KMSKey != "" && (r.Header.Get("X-Amz-Server-Side-Encryption") != "aws:kms" || r.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != test.sse.KMSKey) {
						t.Error("missing KMS encryption headers")
					}
					var err error
					uploaded, err = io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
				case http.MethodHead:
					w.Header().Set("Content-Length", fmt.Sprint(len(uploaded)))
				case http.MethodGet:
					w.Header().Set("Content-Length", fmt.Sprint(len(uploaded)))
					_, _ = w.Write(uploaded)
				default:
					t.Errorf("unexpected request %s", r.Method)
				}
			}))
			defer server.Close()
			store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{
				Endpoint: server.URL, Region: "eu-west-1", Key: "test-key", Secret: "test-secret",
			}, SSE: test.sse})
			if err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(t.TempDir(), "input")
			body := []byte("S3 compatible transfer fixture")
			if err := os.WriteFile(input, body, 0600); err != nil {
				t.Fatal(err)
			}
			object, err := store.Put(t.Context(), "s3://bucket/dir/object", input)
			if err != nil {
				t.Fatal(err)
			}
			if object.Size != int64(len(body)) || !bytes.Equal(uploaded, body) {
				t.Fatal("upload changed file contents or size")
			}
			output := filepath.Join(t.TempDir(), "output")
			if _, err := store.Get(t.Context(), "s3://bucket/dir/object", output); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, body) {
				t.Fatalf("download = %q", got)
			}
		})
	}
}

func TestAmazonS3ListPagination(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/bucket" || r.URL.Query().Get("prefix") != "dir" {
			t.Errorf("unexpected list URL %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		if requests == 1 {
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next-page</NextContinuationToken><Contents><Key>dir/</Key></Contents><Contents><Key>dir/a</Key><Size>1</Size><ETag>a</ETag><LastModified>2006-01-02T15:04:05Z</LastModified></Contents></ListBucketResult>`)
		} else {
			if r.URL.Query().Get("continuation-token") != "next-page" {
				t.Error("missing next page token")
			}
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>dir/b</Key><Size>2</Size><ETag>b</ETag><LastModified>2006-01-02T15:04:05Z</LastModified></Contents></ListBucketResult>`)
		}
	}))
	defer server.Close()
	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{Endpoint: server.URL, Region: "local", DisableAutoCredentialLoad: true}})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := store.List(t.Context(), "s3://bucket/dir")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(objects) != 2 || objects[0].URL != "s3://bucket/dir/a" || objects[1].Size != 2 {
		t.Fatalf("unexpected paginated list %#v, requests %d", objects, requests)
	}
}

func TestAmazonS3ListPreservesEndpointQualifiedURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			if r.URL.Path != "/bucket" {
				t.Errorf("unexpected bucket discovery URL %s", r.URL)
			}
			w.Header().Set("X-Amz-Bucket-Region", "us-east-1")
		case http.MethodGet:
			if r.URL.Path != "/bucket" || r.URL.Query().Get("prefix") != "dir/" {
				t.Errorf("unexpected list URL %s", r.URL)
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>dir/file.txt</Key><Size>4</Size><ETag>file</ETag><LastModified>2006-01-02T15:04:05Z</LastModified></Contents></ListBucketResult>`)
		default:
			t.Errorf("unexpected request %s", r.Method)
		}
	}))
	defer server.Close()

	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{
		Region: "us-east-1", DisableAutoCredentialLoad: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	serverURL := server.URL
	store.cfg.BaseEndpoint = &serverURL
	store.forcePathStyle = true

	objects, err := store.List(t.Context(), "s3://s3.amazonaws.com/bucket/dir/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 {
		t.Fatalf("listed %d objects, want 1", len(objects))
	}
	if objects[0].URL != "s3://s3.amazonaws.com/bucket/dir/file.txt" {
		t.Fatalf("listed URL = %q", objects[0].URL)
	}
}

func TestAmazonS3RegionDiscovery(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusMovedPermanently, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("region discovery was signed")
				}
				w.Header().Set("X-Amz-Bucket-Region", "eu-west-1")
				w.WriteHeader(status)
			}))
			defer server.Close()
			store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{Endpoint: server.URL, Key: "key", Secret: "secret"}})
			if err != nil {
				t.Fatal(err)
			}
			_, region, err := store.parse("s3://bucket/key")
			if err != nil {
				t.Fatal(err)
			}
			if region != "eu-west-1" {
				t.Fatalf("region = %q", region)
			}
		})
	}
}

func TestAmazonS3RegionDiscoveryDefaultsWhenHeaderMissing(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_MAX_ATTEMPTS", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	var uploads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "" && !strings.Contains(authorization, "/us-east-1/s3/") {
			t.Errorf("signed request used wrong region: %q", authorization)
		}
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/bucket":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead && r.URL.Path == "/bucket/object":
			w.Header().Set("Content-Length", "7")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/bucket/object":
			w.Header().Set("Content-Length", "7")
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", "bytes 0-6/7")
				w.WriteHeader(http.StatusPartialContent)
			}
			_, _ = io.WriteString(w, "fixture")
		case r.Method == http.MethodPut && r.URL.Path == "/bucket/object":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if string(data) != "fixture" {
				t.Errorf("uploaded %q, want fixture", data)
			}
			uploads.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected S3 request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{
		Endpoint: server.URL,
		Key:      "fixture",
		Secret:   "secret",
	}})
	if err != nil {
		t.Fatal(err)
	}

	stat, err := store.Stat(t.Context(), "s3://bucket/object")
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size != 7 || stat.ETag != `"fixture"` {
		t.Fatalf("stat = %#v, want fixture metadata", stat)
	}

	output := filepath.Join(t.TempDir(), "output")
	if _, err := store.Get(t.Context(), "s3://bucket/object", output); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fixture" {
		t.Fatalf("download = %q, want fixture", got)
	}

	input := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(input, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "s3://bucket/object", input); err != nil {
		t.Fatal(err)
	}
	if got := uploads.Load(); got != 1 {
		t.Fatalf("upload requests = %d, want 1", got)
	}
}

func TestAmazonS3PreservesRetryAttempts(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	for _, test := range []struct {
		name        string
		maxRetries  int32
		maxAttempts string
		want        int
	}{
		{name: "legacy default", want: 4},
		{name: "configured retries", maxRetries: 7, want: 8},
		{name: "SDK environment override", maxAttempts: "6", want: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AWS_MAX_ATTEMPTS", test.maxAttempts)
			store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{
				Region:     "us-east-1",
				Key:        "fixture",
				Secret:     "secret",
				MaxRetries: test.maxRetries,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if store.cfg.RetryMaxAttempts != test.want {
				t.Fatalf("retry attempts = %d, want %d", store.cfg.RetryMaxAttempts, test.want)
			}
		})
	}
}

func TestAmazonS3DownloadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", "7")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("ETag", `"fixture"`)
	}))
	defer server.Close()
	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{Endpoint: server.URL, Region: "local", DisableAutoCredentialLoad: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "s3://bucket/object", filepath.Join(t.TempDir(), "file")); err == nil {
		t.Fatal("failed download reported success")
	}
}

func TestAmazonS3CustomerKeyFallback(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("01234567890123456789012345678901"), 0600); err != nil {
		t.Fatal(err)
	}
	var encryptedRequests, plainRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Server-Side-Encryption-Customer-Key") != "" {
			encryptedRequests++
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		plainRequests++
		w.Header().Set("Content-Length", "7")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("ETag", `"fixture"`)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "release")
		}
	}))
	defer server.Close()
	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{Endpoint: server.URL, Region: "local", DisableAutoCredentialLoad: true}, SSE: &config.SSE{CustomerKeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "output")
	if _, err := store.Get(t.Context(), "s3://bucket/object", path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "release" || encryptedRequests != 2 || plainRequests != 2 {
		t.Fatalf("fallback contents %q, encrypted requests %d, plain requests %d", got, encryptedRequests, plainRequests)
	}
}

func TestAmazonS3MultipartTransfer(t *testing.T) {
	body := bytes.Repeat([]byte("part-data"), 700000)
	var lock sync.Mutex
	parts := map[int][]byte{}
	var completed, downloads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		if r.Method == http.MethodPost {
			if r.URL.Query().Has("uploads") {
				_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>upload-fixture</UploadId></InitiateMultipartUploadResult>`)
				return
			}
			lock.Lock()
			defer lock.Unlock()
			completed++
			if !bytes.Equal(append(append([]byte{}, parts[1]...), parts[2]...), body) {
				t.Error("multipart upload changed contents")
			}
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>fixture</ETag></CompleteMultipartUploadResult>`)
			return
		}
		if r.Method == http.MethodPut {
			part, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
			if err != nil {
				t.Error(err)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			lock.Lock()
			parts[part] = data
			lock.Unlock()
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			return
		}
		var first, last int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if last >= len(body) {
			last = len(body) - 1
		}
		lock.Lock()
		downloads++
		lock.Unlock()
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, len(body)))
		w.Header().Set("Content-Length", fmt.Sprint(last-first+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[first : last+1])
	}))
	defer server.Close()
	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{Endpoint: server.URL, Region: "local", DisableAutoCredentialLoad: true}})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(input, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "s3://bucket/object", input); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "output")
	if _, err := store.Get(t.Context(), "s3://bucket/object", output); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) || len(parts) != 2 || completed != 1 || downloads != 2 {
		t.Fatalf("multipart transfer incomplete, upload parts %d, completion %d, download parts %d", len(parts), completed, downloads)
	}
}

func TestAmazonS3RetryCount(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{Endpoint: server.URL, Region: "local", MaxRetries: 1, DisableAutoCredentialLoad: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(t.Context(), "s3://bucket/object"); err == nil {
		t.Fatal("unavailable server reported success")
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want initial request plus one retry", requests)
	}
}
