package storage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func writeRootDownloadedFilePlatform(ctx context.Context, parent *os.Root, name string, write func(*os.File) error) error {
	dir, err := parent.Open(".")
	if err != nil {
		return fmt.Errorf("pin destination directory: %w", err)
	}
	defer dir.Close() //nolint:errcheck // The pinned destination directory has no pending writes when it is closed.
	mountpoint, err := isMountpointDirectory(dir)
	if err != nil {
		return fmt.Errorf("identify destination filesystem: %w", err)
	}
	if !mountpoint {
		return writeRootFileAtomically(ctx, parent, name, write)
	}
	return writeMountpointDownload(ctx, dir, name, write)
}

func isMountpointDirectory(dir *os.File) (bool, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return false, err
	}
	if stat.Type != unix.FUSE_SUPER_MAGIC {
		return false, nil
	}
	fdinfo, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", dir.Fd()))
	if err != nil {
		return false, err
	}
	var mountID string
	for _, line := range strings.Split(string(fdinfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			if _, err := strconv.ParseUint(fields[1], 10, 64); err != nil {
				return false, fmt.Errorf("invalid directory mount ID: %w", err)
			}
			mountID = fields[1]
			break
		}
	}
	if mountID == "" {
		return false, errors.New("directory mount ID is unavailable")
	}
	mounts, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer mounts.Close() //nolint:errcheck // Closing the read-only proc metadata stream cannot change the mount decision.
	return mountpointMountID(mounts, mountID)
}

func mountpointMountID(mounts io.Reader, mountID string) (bool, error) {
	scanner := bufio.NewScanner(mounts)
	for scanner.Scan() {
		left, right, ok := strings.Cut(scanner.Text(), " - ")
		fields := strings.Fields(left)
		if len(fields) == 0 || fields[0] != mountID {
			continue
		}
		filesystem := strings.Fields(right)
		if !ok || len(fields) < 6 || len(filesystem) < 3 {
			return false, errors.New("invalid mountinfo entry for destination directory")
		}
		return filesystem[0] == "fuse" && filesystem[1] == "mountpoint-s3", nil
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, errors.New("destination mount ID is absent from mountinfo")
}

// Mountpoint cannot chmod files or atomically replace objects in general purpose
// buckets. Source failures leave the destination intact, but errors after the
// mounted copy starts cannot roll back.
func writeMountpointDownload(ctx context.Context, parent *os.File, name string, write func(*os.File) error) (retErr error) {
	temporary, err := os.Open(os.TempDir())
	if err != nil {
		return fmt.Errorf("open local temporary directory: %w", err)
	}
	defer temporary.Close() //nolint:errcheck // The temporary directory handle only scopes staging path creation.
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(temporary.Fd()), &filesystem); err != nil {
		return fmt.Errorf("identify temporary directory filesystem: %w", err)
	}
	if filesystem.Type == unix.FUSE_SUPER_MAGIC {
		return errors.New("download staging requires a non-FUSE temporary directory")
	}
	stage, err := os.CreateTemp(fmt.Sprintf("/proc/self/fd/%d", temporary.Fd()), ".funnel-download-*")
	if err != nil {
		return fmt.Errorf("create local download stage: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, stage.Close(), os.Remove(stage.Name()))
	}()
	if err := stage.Chmod(0600); err != nil {
		return fmt.Errorf("restrict local download stage: %w", err)
	}
	private, err := stage.Stat()
	if err != nil {
		return fmt.Errorf("stat private download stage: %w", err)
	}
	if private.Mode().Perm() != 0600 {
		return fmt.Errorf("download stage permissions are %04o, want 0600", private.Mode().Perm())
	}
	if err := errors.Join(write(stage), ctx.Err()); err != nil {
		return fmt.Errorf("stage download: %w", err)
	}
	if err := stage.Sync(); err != nil {
		return fmt.Errorf("sync local download stage: %w", err)
	}
	info, err := stage.Stat()
	if err != nil {
		return fmt.Errorf("stat local download stage: %w", err)
	}
	if _, err := stage.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind local download stage: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{
		Flags:   unix.O_WRONLY | unix.O_CREAT | unix.O_TRUNC | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Mode:    0666,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return fmt.Errorf("open mounted destination: %w", err)
	}
	destination := os.NewFile(uintptr(fd), name)
	_, copyErr := io.CopyN(destination, &downloadStageReader{ctx: ctx, file: stage}, info.Size())
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if err := errors.Join(copyErr, syncErr, closeErr, ctx.Err()); err != nil {
		return fmt.Errorf("publish mounted download: %w", err)
	}
	return nil
}

type downloadStageReader struct {
	ctx  context.Context
	file *os.File
}

func (r *downloadStageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}
