//go:build linux

package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const requireLocalOwnershipTestEnv = "FUNNEL_TEST_LOCAL_OWNERSHIP"

func TestLocalTransfersPreserveExecutorPermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		if os.Getenv(requireLocalOwnershipTestEnv) == "1" {
			t.Fatalf("%s=1 requires the test process to run as root", requireLocalOwnershipTestEnv)
		}
		t.Skip("requires root to create files owned by another UID and GID")
	}

	for _, test := range []struct {
		name          string
		operation     string
		stagingLink   string
		parentLink    bool
		existingUID   uint32
		sourceUID     uint32
		sourceGID     uint32
		sourceMode    os.FileMode
		executorUID   uint32
		executorGID   uint32
		mustWriteCopy bool
	}{
		{name: "owner access and independent copy", sourceUID: 65534, sourceGID: 65534, sourceMode: 0600, executorUID: 65534, executorGID: 65534, mustWriteCopy: true},
		{name: "group read access", sourceUID: 65534, sourceGID: 65534, sourceMode: 0640, executorUID: 65533, executorGID: 65534},
		{name: "owner access through dangling staging symlink", stagingLink: "target.txt", sourceUID: 65534, sourceGID: 65534, sourceMode: 0600, executorUID: 65534, executorGID: 65534, mustWriteCopy: true},
		{name: "group access through parent relative staging symlink", stagingLink: "../target.txt", sourceUID: 65534, sourceGID: 65534, sourceMode: 0640, executorUID: 65533, executorGID: 65534},
		{name: "existing destination retains metadata", existingUID: 65533, sourceUID: 65534, sourceGID: 65534, sourceMode: 0640, executorUID: 65533, executorGID: 65534, mustWriteCopy: true},
		{name: "Put symlinked parent resolution", operation: "Put", stagingLink: "../target.txt", parentLink: true, sourceUID: 65534, sourceGID: 65534, sourceMode: 0640, executorUID: 65533, executorGID: 65534},
		{name: "Put owner access", operation: "Put", sourceUID: 65534, sourceGID: 65534, sourceMode: 0600, executorUID: 65534, executorGID: 65534, mustWriteCopy: true},
		{name: "Put group access through confined staging symlink", operation: "Put", stagingLink: "../target.txt", sourceUID: 65534, sourceGID: 65534, sourceMode: 0640, executorUID: 65533, executorGID: 65534},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, err := os.MkdirTemp("/tmp", "funnel-local-ownership-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(base); err != nil {
					t.Error(err)
				}
			})
			if err := os.Chmod(base, 0755); err != nil {
				t.Fatal(err)
			}
			allowed := filepath.Join(base, "allowed")
			if err := os.Mkdir(allowed, 0755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(allowed, "private.txt")
			staged := filepath.Join(base, "staging", "private.txt")
			if test.operation == "Put" {
				staged = filepath.Join(allowed, "staging", "private.txt")
			}
			if test.parentLink {
				parent := filepath.Dir(filepath.Dir(staged))
				if err := os.MkdirAll(filepath.Join(parent, "real", "sub"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("real/sub", filepath.Dir(staged)); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(filepath.Dir(staged), 0755); err != nil {
				t.Fatal(err)
			}
			if test.stagingLink != "" {
				if err := os.Symlink(test.stagingLink, staged); err != nil {
					t.Fatal(err)
				}
			}
			if test.existingUID != 0 {
				if err := os.WriteFile(staged, []byte("previous contents"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(staged, int(test.existingUID), int(test.executorGID)); err != nil {
					t.Fatal(err)
				}
			}
			const original = "source contents\n"
			if err := os.WriteFile(source, []byte(original), test.sourceMode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(source, int(test.sourceUID), int(test.sourceGID)); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(source, test.sourceMode); err != nil {
				t.Fatal(err)
			}
			sourceAsExecutor, err := runLocalStorageIdentity(test.executorUID, test.executorGID, "/bin/cat", source)
			if err != nil {
				t.Fatalf("executor cannot read source fixture: %v", err)
			}
			if string(sourceAsExecutor) != original {
				t.Fatalf("executor read source contents %q, want %q", sourceAsExecutor, original)
			}

			local := &Local{allowedDirs: []string{allowed}}
			oldUmask := syscall.Umask(0077)
			var getErr error
			if test.operation == "Put" {
				_, getErr = local.Put(t.Context(), staged, source)
			} else {
				_, getErr = local.Get(t.Context(), source, staged)
			}
			syscall.Umask(oldUmask)
			if getErr != nil {
				t.Fatalf("Get source into staging: %v", getErr)
			}
			stagedInfo, err := os.Stat(staged)
			if err != nil {
				t.Fatal(err)
			}
			expectedMode := test.sourceMode
			if test.existingUID != 0 {
				expectedMode = 0600
				stat := stagedInfo.Sys().(*syscall.Stat_t)
				if stat.Uid != test.existingUID || stat.Gid != test.executorGID {
					t.Fatalf("existing destination owner changed to %d:%d", stat.Uid, stat.Gid)
				}
			}
			if stagedInfo.Mode().Perm() != expectedMode {
				t.Fatalf("staged permissions = %04o, want %04o", stagedInfo.Mode().Perm(), expectedMode)
			}

			got, err := runLocalStorageIdentity(test.executorUID, test.executorGID, "/bin/cat", staged)
			if err != nil {
				t.Fatalf("executor cannot read staged copy: %v", err)
			}
			if string(got) != original {
				t.Fatalf("executor read staged contents %q, want %q", got, original)
			}
			if test.mustWriteCopy {
				if _, err := runLocalStorageIdentity(test.executorUID, test.executorGID, "/bin/sh", "-c", "printf staged-change >> \"$1\"", "sh", staged); err != nil {
					t.Fatalf("executor cannot write staged copy: %v", err)
				}
				if got, err := os.ReadFile(staged); err != nil || string(got) != original+"staged-change" {
					t.Fatalf("staged contents after executor write = %q, %v", got, err)
				}
			}

			sourceBody, err := os.ReadFile(source)
			if err != nil || string(sourceBody) != original {
				t.Fatalf("source contents after staging = %q, %v", sourceBody, err)
			}
			sourceInfo, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			if sourceInfo.Mode().Perm() != test.sourceMode {
				t.Fatalf("source permissions = %04o, want %04o", sourceInfo.Mode().Perm(), test.sourceMode)
			}
			sourceStat, ok := sourceInfo.Sys().(*syscall.Stat_t)
			if !ok {
				t.Fatalf("source filesystem metadata has type %T, want *syscall.Stat_t", sourceInfo.Sys())
			}
			if sourceStat.Uid != test.sourceUID || sourceStat.Gid != test.sourceGID {
				t.Fatalf("source owner = %d:%d, want %d:%d", sourceStat.Uid, sourceStat.Gid, test.sourceUID, test.sourceGID)
			}
		})
	}
}

func runLocalStorageIdentity(uid, gid uint32, name string, args ...string) ([]byte, error) {
	command := exec.Command(name, args...)
	command.Dir = "/tmp"
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{gid}}}
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s as %d:%d failed: %w: %s", name, uid, gid, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
