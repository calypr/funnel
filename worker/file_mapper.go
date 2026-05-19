package worker

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util/fsutil"
	proto "google.golang.org/protobuf/proto"
)

// FileMapper is responsible for mapping paths into a working directory on the
// worker's host file system.
//
// Every task needs it's own directory to work in. When a file is downloaded for
// a task, it needs to be stored in the task's working directory. Similar for task
// outputs, uploads, stdin/out/err, etc. FileMapper helps the worker engine
// manage all these paths.
type FileMapper struct {
	Volumes      []Volume
	InputVolumes []Volume // tracks volumes added via AddInput; used by consolidateVolumes
	Inputs       []*tes.Input
	Outputs      []*tes.Output
	WorkDir      string
	ScratchDir   string
}

// Volume represents a volume mounted into a docker container.
// This includes a HostPath, the path on the host file system,
// and a ContainerPath, the path on the container file system,
// and whether the volume is read-only.
type Volume struct {
	// The path in tes worker.
	HostPath string
	// The path in Docker.
	ContainerPath string
	Readonly      bool
}

// NewFileMapper returns a new FileMapper, which maps files into the given
// base directory.
func NewFileMapper(dir string) *FileMapper {
	dir, _ = filepath.Abs(dir)
	return &FileMapper{
		Volumes:      []Volume{},
		InputVolumes: []Volume{},
		Inputs:       []*tes.Input{},
		Outputs:      []*tes.Output{},
		WorkDir:      dir,
	}
}

// MapTask adds all the volumes, inputs, and outputs in the given Task to the FileMapper.
func (mapper *FileMapper) MapTask(task *tes.Task) error {
	// Validate working directory
	if !filepath.IsAbs(mapper.WorkDir) {
		return fmt.Errorf("Mapper.WorkDir is not an absolute path")
	}

	// Create the working directory
	err := fsutil.EnsureDir(mapper.WorkDir)
	if err != nil {
		return err
	}

	// Add all the volumes to the mapper
	for _, vol := range task.Volumes {
		err = mapper.AddTmpVolume(vol)
		if err != nil {
			return err
		}
	}

	err = mapper.AddTmpVolume("/tmp")
	if err != nil {
		return err
	}

	// Add all the inputs to the mapper
	for _, input := range task.Inputs {
		err = mapper.AddInput(input)
		if err != nil {
			return err
		}
	}

	// Add all the outputs to the mapper
	for _, output := range task.Outputs {
		err = mapper.AddOutput(output)
		if err != nil {
			return err
		}
	}

	// Consolidate read-only input volumes into the fewest possible ancestor
	// directory mounts. This eliminates file-level bind mounts (which cause
	// EBUSY when a task script calls mv/unlink on a mounted file path) and
	// shrinks the nerdctl/mounts container label.
	mapper.consolidateVolumes()

	return nil
}

func (mapper *FileMapper) CopyInputsToScratch(scratchDir string) error {
	scratchAbsDir, err := filepath.Abs(scratchDir)
	if err != nil {
		return err
	}

	// Copy the input file or directory to the scratch target
	for _, input := range mapper.Inputs {
		scratchTarget := filepath.Join(scratchAbsDir, input.Path)

		info, err := os.Stat(input.Path)
		if err != nil {
			fmt.Println(err)
		}
		if info.IsDir() {
			// Ensure the scratch target directory exists
			if err := os.MkdirAll(scratchTarget, 0755); err != nil {
				return fmt.Errorf("failed to create scratch directory: %w", err)
			}
			copyDir(input.Path, scratchTarget)
		} else {
			// Ensure the scratch target directory exists
			if err := os.MkdirAll(path.Dir(scratchTarget), 0755); err != nil {
				return fmt.Errorf("failed to create scratch directory: %w", err)
			}
			err = copyFile(input.Path, scratchTarget)
			if err != nil {
				return fmt.Errorf("failed to copy input file to scratch directory: %w", err)
			}
			_, _ = os.ReadFile(scratchTarget)
		}
	}

	return nil
}

func (mapper *FileMapper) CopyOutputsToWorkDir(scratchDir string) error {
	scratchAbsDir, err := filepath.Abs(scratchDir)
	if err != nil {
		return err
	}
	err = filepath.Walk(scratchAbsDir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return nil
		})
	if err != nil {
		fmt.Println(err)
	}

	// Copy the input file or directory to the scratch target
	for _, output := range mapper.Outputs {
		scratchTarget := filepath.Join(scratchAbsDir, output.Path)

		matches, err := filepath.Glob(scratchTarget)
		if err != nil {
			return fmt.Errorf("invalid pattern %s: %w", scratchTarget, err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("no files matched the pattern: %s", scratchTarget)
		}

		parentDir := filepath.Dir(output.Path)
		for _, src := range matches {
			info, err := os.Stat(src)
			if err != nil {
				return fmt.Errorf("failed to stat output path: %w", err)
			}
			// If output is a directory
			if info.IsDir() {
				// Ensure the scratch target directory exists
				if err = os.MkdirAll(output.Path, 0755); err != nil {
					return fmt.Errorf("failed to create output path: %w", err)
				}
				copyDir(src, output.Path)
				// If output is a file
			} else {
				// Ensure the scratch target directory exists
				if err = os.MkdirAll(path.Dir(parentDir), 0755); err != nil {
					return fmt.Errorf("failed to create output path: %w", err)
				}

				workDirPath := filepath.Join(parentDir, info.Name())

				err = copyFile(src, workDirPath)
				if err != nil {
					return fmt.Errorf("failed to copy output file to output directory: %w", err)
				}
			}
		}
	}

	return nil
}

func copyDir(src string, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src string, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// AddVolume adds a mapped volume to the mapper. A corresponding Volume record
// is added to mapper.Volumes.
//
// Returns (true, nil) when the volume was added or replaced an existing one.
// Returns (false, nil) when the volume was skipped because it is already
// covered by an existing mount (duplicate or subpath of an existing RW volume).
// Returns (false, err) when the paths are invalid.
func (mapper *FileMapper) AddVolume(hostPath string, mountPoint string, readonly bool) (bool, error) {
	vol := Volume{
		HostPath:      hostPath,
		ContainerPath: mountPoint,
		Readonly:      readonly,
	}

	for i, v := range mapper.Volumes {
		// Volume is already present — nothing to do.
		if vol == v {
			return false, nil
		}

		// If the proposed RW volume is a subpath of an existing RW volume,
		// the path is already reachable — skip it.
		// If an existing RW volume is a subpath of the proposed RW volume,
		// replace the narrower mount with the wider one.
		if !vol.Readonly && !v.Readonly {
			if mapper.IsSubpath(vol.ContainerPath, v.ContainerPath) {
				return false, nil
			} else if mapper.IsSubpath(v.ContainerPath, vol.ContainerPath) {
				mapper.Volumes[i] = vol
				return true, nil
			}
		}
	}

	mapper.Volumes = append(mapper.Volumes, vol)
	return true, nil
}

// HostPath returns a mapped path.
//
// The path is concatenated to the mapper's base dir.
// e.g. If the mapper is configured with a base dir of "/tmp/mapped_files", then
// mapper.HostPath("/home/ubuntu/myfile") will return "/tmp/mapped_files/home/ubuntu/myfile".
//
// The mapped path is required to be a subpath of the mapper's base directory.
// e.g. mapper.HostPath("../../foo") should fail with an error.
func (mapper *FileMapper) HostPath(src string) (string, error) {
	p := path.Join(mapper.WorkDir, src)
	p = path.Clean(p)
	if !mapper.IsSubpath(p, mapper.WorkDir) {
		return "", fmt.Errorf("Invalid path: %s is not a valid subpath of %s", p, mapper.WorkDir)
	}
	return p, nil
}

func (mapper *FileMapper) HostScratchPath(src string) (string, error) {
	p := path.Join(mapper.ScratchDir, src)
	p = path.Clean(p)
	if !mapper.IsSubpath(p, mapper.ScratchDir) {
		return "", fmt.Errorf("Invalid path: %s is not a valid subpath of %s", p, mapper.ScratchDir)
	}
	return p, nil
}

// OpenHostFile opens a file on the host file system at a mapped path.
// "src" is an unmapped path. This function will handle mapping the path.
//
// # This function calls os.Open
//
// If the path can't be mapped or the file can't be opened, an error is returned.
func (mapper *FileMapper) OpenHostFile(src string) (*os.File, error) {
	p, perr := mapper.HostPath(src)
	if perr != nil {
		return nil, perr
	}
	f, oerr := os.Open(p)
	if oerr != nil {
		return nil, oerr
	}
	return f, nil
}

// CreateHostFile creates a file on the host file system at a mapped path.
// "src" is an unmapped path. This function will handle mapping the path.
//
// # This function calls os.Create
//
// If the path can't be mapped or the file can't be created, an error is returned.
func (mapper *FileMapper) CreateHostFile(src string) (*os.File, error) {
	var p string
	var perr error
	if mapper.ScratchDir != "" {
		p, perr = mapper.HostScratchPath(src)
	} else {
		p, perr = mapper.HostPath(src)
	}
	if perr != nil {
		return nil, perr
	}
	err := fsutil.EnsurePath(p)
	if err != nil {
		return nil, err
	}
	f, oerr := os.Create(p)
	if oerr != nil {
		return nil, oerr
	}
	return f, nil
}

// AddTmpVolume creates a directory on the host based on the declared path in
// the container and adds it to mapper.Volumes.
//
// If the path can't be mapped, an error is returned.
func (mapper *FileMapper) AddTmpVolume(mountPoint string) error {
	hostPath, err := mapper.HostPath(mountPoint)
	if err != nil {
		return err
	}

	err = fsutil.EnsureDir(hostPath)
	if err != nil {
		return err
	}

	_, err = mapper.AddVolume(hostPath, mountPoint, false)
	if err != nil {
		return err
	}
	return nil
}

// AddInput adds an input to the mapped files for the given tes.Input.
// The volume is registered as read-write; consolidateVolumes() will later
// replace individual file mounts with a single ancestor directory mount to
// avoid EBUSY errors when task scripts call mv/unlink on a mounted path.
// Successfully added volumes are tracked in mapper.InputVolumes so that
// consolidateVolumes() can identify them without relying on the Readonly flag.
// A copy of the tes.Input will be added to mapper.Inputs, with the
// "Path" field updated to the mapped host path.
//
// If the path can't be mapped an error is returned.
func (mapper *FileMapper) AddInput(input *tes.Input) error {
	hostPath, err := mapper.HostPath(input.Path)
	if err != nil {
		return err
	}

	err = fsutil.EnsurePath(hostPath)
	if err != nil {
		return err
	}

	// Add input volume as read-write; track it so consolidateVolumes() can
	// identify input volumes without using the Readonly flag as a classifier.
	added, err := mapper.AddVolume(hostPath, input.Path, false)
	if err != nil {
		return err
	}
	if added {
		mapper.InputVolumes = append(mapper.InputVolumes, Volume{
			HostPath:      hostPath,
			ContainerPath: input.Path,
		})
	}

	// If 'content' field is set create the file
	if input.Content != "" {
		err := os.WriteFile(hostPath, []byte(input.Content), 0775)
		if err != nil {
			return fmt.Errorf("Error writing content of task input to file %v", err)
		}
		return nil
	}

	// Create a tes.Input for the input with a path mapped to the host
	hostIn := proto.Clone(input).(*tes.Input)
	hostIn.Path = hostPath
	mapper.Inputs = append(mapper.Inputs, hostIn)
	return nil
}

// AddOutput adds an output to the mapped files for the given tes.Output.
// A copy of the tes.Output will be added to mapper.Outputs, with the
// "Path" field updated to the mapped host path.
//
// If the path can't be mapped, an error is returned.
func (mapper *FileMapper) AddOutput(output *tes.Output) error {
	hostPath, err := mapper.HostPath(output.Path)
	if err != nil {
		return err
	}

	hostDir := hostPath
	mountDir := output.Path
	if output.Type == tes.FileType_FILE {
		hostDir = path.Dir(hostPath)
		mountDir = path.Dir(output.Path)
	}

	err = fsutil.EnsureDir(hostDir)
	if err != nil {
		return err
	}

	// Add output volumes
	_, err = mapper.AddVolume(hostDir, mountDir, false)
	if err != nil {
		return err
	}

	// Create a tes.Output for the out with a path mapped to the host
	hostOut := proto.Clone(output).(*tes.Output)
	hostOut.Path = hostPath
	mapper.Outputs = append(mapper.Outputs, hostOut)
	return nil
}

// IsSubpath returns true if the given path "p" is a subpath of "base".
func (mapper *FileMapper) IsSubpath(p string, base string) bool {
	return strings.HasPrefix(p, base)
}

// ContainerPath returns an unmapped path.
//
// The mapper's base dir is stripped from the path.
// e.g. If the mapper is configured with a base dir of "/tmp/mapped_files", then
// mapper.ContainerPath("/tmp/mapped_files/home/ubuntu/myfile") will return "/home/ubuntu/myfile".
func (mapper *FileMapper) ContainerPath(src string) string {
	p := strings.TrimPrefix(src, mapper.WorkDir)
	p = path.Clean("/" + p)
	return p
}

// mergeVolume inserts vol into vols using the same RW-deduplication rules as
// AddVolume: skip if already covered by an existing mount, replace a narrower
// existing mount with the wider one, otherwise append.
func mergeVolume(vols []Volume, vol Volume, mapper *FileMapper) []Volume {
	for i, v := range vols {
		if vol == v {
			return vols
		}
		if !vol.Readonly && !v.Readonly {
			if mapper.IsSubpath(vol.ContainerPath, v.ContainerPath) {
				return vols
			}
			if mapper.IsSubpath(v.ContainerPath, vol.ContainerPath) {
				vols[i] = vol
				return vols
			}
		}
	}
	return append(vols, vol)
}

// consolidateVolumes optimizes container mounts by replacing individual input
// file mounts with a single read-write ancestor directory mount.
//
// Mounting individual input files causes EBUSY errors when task scripts call
// mv or unlink (the kernel refuses to unlink a bind-mount point). Mounting
// the deepest common ancestor directory instead lets the task freely manipulate
// inputs. It also reduces mount count and shrinks the nerdctl/mounts label
// when many input files share a common directory prefix.
//
// Input volumes are identified via mapper.InputVolumes (populated by AddInput),
// not by the Readonly flag, so that Readonly retains its literal meaning
// ("mount this path read-only in the container").
//
// Algorithm:
//  1. Build the set of non-input volumes (tmp dirs, output dirs) to keep.
//  2. Find the deepest common directory ancestor of all input container paths.
//  3. Replace the entire input set with one read-write mount at that ancestor.
//  4. Fall back to per-volume parent-dir promotion when inputs span disjoint
//     subtrees and the ancestor collapses to root.
func (mapper *FileMapper) consolidateVolumes() {
	if len(mapper.InputVolumes) == 0 {
		return
	}

	// Build a set of input container paths so we can separate input volumes
	// from tmp/output volumes in mapper.Volumes.
	inputSet := map[string]bool{}
	for _, v := range mapper.InputVolumes {
		inputSet[v.ContainerPath] = true
	}

	var nonInputVols []Volume
	for _, v := range mapper.Volumes {
		if !inputSet[v.ContainerPath] {
			nonInputVols = append(nonInputVols, v)
		}
	}

	// Find the deepest common directory ancestor of all input container paths.
	contPaths := make([]string, len(mapper.InputVolumes))
	for i, v := range mapper.InputVolumes {
		contPaths[i] = v.ContainerPath
	}
	ancestor := volumeCommonDirAncestor(contPaths)

	if ancestor != "" && ancestor != "/" && ancestor != "." {
		// All inputs share a common ancestor — one mount covers them all.
		// WorkDir+ancestor is valid by the HostPath() construction invariant
		// (every input host path == WorkDir + container path).
		inputAncestorVol := Volume{
			HostPath:      filepath.Join(mapper.WorkDir, ancestor),
			ContainerPath: ancestor,
			Readonly:      false,
		}
		nonInputVols = mergeVolume(nonInputVols, inputAncestorVol, mapper)
	} else {
		// Inputs span disjoint subtrees; fall back to promoting each to its
		// immediate parent directory to at least eliminate file-level mounts.
		seen := map[string]bool{}
		for _, v := range mapper.InputVolumes {
			contParent := filepath.Dir(v.ContainerPath)
			hostParent := filepath.Dir(v.HostPath)
			key := hostParent + ":" + contParent
			if seen[key] {
				continue
			}
			seen[key] = true
			nonInputVols = mergeVolume(nonInputVols, Volume{
				HostPath:      hostParent,
				ContainerPath: contParent,
				Readonly:      false,
			}, mapper)
		}
	}
	mapper.Volumes = nonInputVols
}

// volumeCommonDirAncestor returns the deepest directory that is a common
// ancestor of the parent directories of all given paths.
// For a single path it returns filepath.Dir of that path (promoting a lone
// file to its containing directory).
func volumeCommonDirAncestor(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	ancestor := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		ancestor = volumeCommonPathPrefix(ancestor, filepath.Dir(p))
		if ancestor == "/" || ancestor == "." {
			break
		}
	}
	return ancestor
}

// volumeCommonPathPrefix returns the longest common directory-component prefix
// of two absolute paths (e.g. "/a/b/c" and "/a/b/d" → "/a/b").
func volumeCommonPathPrefix(a, b string) string {
	aParts := strings.Split(strings.TrimPrefix(a, "/"), "/")
	bParts := strings.Split(strings.TrimPrefix(b, "/"), "/")
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	var common []string
	for i := 0; i < n; i++ {
		if aParts[i] != bParts[i] {
			break
		}
		common = append(common, aParts[i])
	}
	if len(common) == 0 {
		return "/"
	}
	return "/" + strings.Join(common, "/")
}

// Cleanup deletes the working directory.
func (mapper *FileMapper) Cleanup() error {
	return os.RemoveAll(mapper.WorkDir)
}
