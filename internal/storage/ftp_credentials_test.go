package storage

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/api/config"
	appconfig "github.com/calypr/funnel/internal/config"
)

// A real loopback endpoint records any credentials disclosed during login.
func ftpCredentialRecorder(t *testing.T) (string, <-chan []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 1)
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !strings.Contains(err.Error(), "closed") {
			t.Error(err)
		}
	})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- nil
			return
		}
		defer conn.Close()                                    //nolint:errcheck // Test endpoint cleanup.
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // Bounds a broken test client.
		_, _ = fmt.Fprint(conn, "220 test FTP\r\n")           //nolint:errcheck // The client assertions diagnose failed communication.
		scanner := bufio.NewScanner(conn)
		var credentials []string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "USER "):
				credentials = append(credentials, line)
				_, _ = fmt.Fprint(conn, "331 password required\r\n") //nolint:errcheck // See banner write.
			case strings.HasPrefix(line, "PASS "):
				credentials = append(credentials, line)
				_, _ = fmt.Fprint(conn, "530 test complete\r\n") //nolint:errcheck // The test checks the recorded credentials.
				done <- credentials
				return
			default:
				_, _ = fmt.Fprint(conn, "500 unsupported\r\n") //nolint:errcheck // See banner write.
			}
		}
		done <- credentials
	}()
	return "ftp://" + listener.Addr().String(), done
}

func TestFTPConfiguredCredentialsRequireBoundEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "ftp://trusted.example:21"} {
		t.Run(endpoint, func(t *testing.T) {
			origin, received := ftpCredentialRecorder(t)
			conf := &config.FTPStorage{User: "configured-user", Password: "configured-secret", Endpoint: endpoint}
			client, err := connect(origin+"/file", conf)
			if client != nil {
				client.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "credential endpoint") {
				t.Errorf("expected credential endpoint rejection before dialing, got %v", err)
			}
			select {
			case creds := <-received:
				if len(creds) > 0 {
					t.Errorf("credentials leaked to untrusted endpoint: %q", creds)
				}
			default:
			}
		})
	}
}

func TestFTPCredentialsReachOnlySelectedOrigin(t *testing.T) {
	for _, name := range []string{"configured", "url", "url-no-password", "anonymous"} {
		t.Run(name, func(t *testing.T) {
			origin, received := ftpCredentialRecorder(t)
			conf := &config.FTPStorage{User: "configured-user", Password: "configured-secret", Endpoint: origin}
			target := origin + "/file"
			want := "USER configured-user|PASS configured-secret"
			if strings.HasPrefix(name, "url") {
				conf.Endpoint = "ftp://other.example"
				target = strings.Replace(target, "ftp://", "ftp://url-user:url-secret@", 1)
				want = "USER url-user|PASS url-secret"
				if name == "url-no-password" {
					target = strings.Replace(target, ":url-secret@", "@", 1)
					want = "USER url-user|PASS "
				}
			}
			if name == "anonymous" {
				conf = appconfig.DefaultConfig().FTPStorage
				want = "USER anonymous|PASS anonymous"
			}
			client, err := connect(target, conf)
			if err == nil || !strings.Contains(err.Error(), "530") {
				t.Errorf("expected test endpoint login rejection, got %v", err)
			}
			if client != nil {
				client.Close()
			}
			select {
			case creds := <-received:
				if strings.Join(creds, "|") != want {
					t.Fatalf("login = %q, want %q", creds, want)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("FTP endpoint received no login")
			}
		})
	}
}

func TestFTPCredentialEndpointConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "https://trusted.example", "ftp://user:secret@trusted.example", "ftp://trusted.example/files", "ftp://trusted.example?x=y", "ftp://trusted.example#fragment", "ftp://trusted.example:99999"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewFTP(&config.FTPStorage{User: "configured-user", Password: "secret", Endpoint: endpoint}); err == nil {
				t.Fatal("accepted invalid credential endpoint")
			}
		})
	}
	for _, endpoint := range []string{"ftp://TRUSTED.example", "ftp://trusted.example:21/", "ftp://[::1]:2121"} {
		if _, err := NewFTP(&config.FTPStorage{User: "configured-user", Password: "secret", Endpoint: endpoint}); err != nil {
			t.Fatal(err)
		}
	}
	for _, conf := range []*config.FTPStorage{nil, {}, {User: "anonymous", Password: "anonymous"}} {
		if _, err := NewFTP(conf); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewFTP(&config.FTPStorage{User: "anonymous", Password: "private-email"}); err == nil {
		t.Fatal("custom anonymous password must be origin-bound")
	}
}

func TestFTPOriginNormalizesHostAndDefaultPort(t *testing.T) {
	a, err := ftpCredentialEndpoint("ftp://TRUSTED.example")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ftpCredentialEndpoint("ftp://trusted.example:21")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same origin did not compare equal: %q, %q", a, b)
	}
	c, err := ftpCredentialEndpoint("ftp://trusted.example:2121")
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("different port compared equal")
	}
}
