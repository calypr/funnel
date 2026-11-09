package hpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type hpcJobBinding struct {
	TaskID  string `json:"task_id"`
	Backend string `json:"backend"`
	JobID   string `json:"job_id"`
}

func bindingSegment(value string) bool {
	return value != "." && filepath.IsLocal(value) && !strings.ContainsAny(value, "/\\") && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validHPCJobID(id string) bool {
	return id != "" && !strings.HasPrefix(id, "-") && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// canonicalBindingPath resolves existing ancestors as well as a not-yet-created leaf.
func canonicalBindingPath(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := canonicalBindingPath(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func (b *Backend) bindingDirectory(taskID string) (string, error) {
	if b.Conf == nil || !filepath.IsAbs(b.Conf.HPCStateDir) {
		return "", fmt.Errorf("HPCStateDir must be an absolute private controller directory")
	}
	if !bindingSegment(taskID) || !bindingSegment(b.Name) {
		return "", fmt.Errorf("invalid HPC task or backend identity")
	}
	state, err := canonicalBindingPath(b.Conf.HPCStateDir)
	if err != nil {
		return "", err
	}
	roots := []string{b.Conf.GetWorker().GetWorkDir(), b.Conf.GetWorker().GetScratchPath()}
	if local := b.Conf.LocalStorage; local != nil && !local.Disabled {
		if len(local.AllowedDirs) == 0 {
			return "", fmt.Errorf("HPCStateDir requires bounded LocalStorage.AllowedDirs or disabled local storage")
		}
		roots = append(roots, local.AllowedDirs...)
	}
	for _, name := range roots {
		if name == "" {
			continue
		}
		root, err := canonicalBindingPath(name)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, state)
		if err != nil {
			return "", err
		}
		reverse, err := filepath.Rel(state, root)
		if err != nil {
			return "", err
		}
		if rel == "." || filepath.IsLocal(rel) || reverse == "." || filepath.IsLocal(reverse) {
			return "", fmt.Errorf("HPCStateDir must not overlap task data or local storage roots")
		}
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return "", err
	}
	info, err := os.Stat(state)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("HPCStateDir must be private (mode 0700)")
	}
	return filepath.Join(state, b.Name), nil
}

// Task logs retain provider IDs for display, but never supply command authority.
func (b *Backend) saveJobBinding(taskID, jobID string) (retErr error) {
	if !validHPCJobID(jobID) {
		return fmt.Errorf("scheduler returned an invalid job ID")
	}
	dir, err := b.bindingDirectory(taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".binding-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() {
		_ = file.Close() //nolint:errcheck // The explicit close below reports commit errors.
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	if err := json.NewEncoder(file).Encode(hpcJobBinding{TaskID: taskID, Backend: b.Name, JobID: jobID}); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, taskID+".json"))
}

func (b *Backend) loadJobBinding(taskID string) (string, error) {
	dir, err := b.bindingDirectory(taskID)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("no trusted HPC submission binding for %s: %w", taskID, err)
	}
	defer root.Close() //nolint:errcheck // Closing a read-only directory cannot change the validated binding.
	data, err := root.ReadFile(taskID + ".json")
	if err != nil {
		return "", fmt.Errorf("no trusted HPC submission binding for %s: %w", taskID, err)
	}
	var binding hpcJobBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return "", err
	}
	if binding.TaskID != taskID || binding.Backend != b.Name || !validHPCJobID(binding.JobID) {
		return "", fmt.Errorf("invalid HPC submission binding for %s", taskID)
	}
	return binding.JobID, nil
}
