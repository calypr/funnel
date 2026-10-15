package storageplan

import (
	"fmt"
	"path"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func validateAbsolutePath(value string) error {
	if !path.IsAbs(value) || path.Clean(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("path %q must be absolute and contain no traversal or empty segments", value)
	}
	return nil
}

func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, "/")+"/") || strings.HasPrefix(b, strings.TrimSuffix(a, "/")+"/")
}

func selectContainer(containers []corev1.Container, name, field string) (int, error) {
	if name == "" {
		if len(containers) != 1 {
			return -1, fmt.Errorf("configure Kubernetes.Storage.%s when the pod template has %d containers", field, len(containers))
		}
		return 0, nil
	}
	for i := range containers {
		if containers[i].Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("Kubernetes.Storage.%s container %q was not found in the pod template", field, name)
}

func validateMount(mount corev1.VolumeMount) error {
	if err := validateAbsolutePath(mount.MountPath); err != nil {
		return err
	}
	if mount.SubPath != "" && (path.IsAbs(mount.SubPath) || path.Clean(mount.SubPath) != mount.SubPath || mount.SubPath == ".." || strings.HasPrefix(mount.SubPath, "../") || strings.ContainsRune(mount.SubPath, '\x00')) {
		return fmt.Errorf("invalid mount subPath %q", mount.SubPath)
	}
	if mount.SubPath != "" && mount.SubPathExpr != "" {
		return fmt.Errorf("mount may not set both subPath and subPathExpr")
	}
	return nil
}

// ApplyWorker adds resolved bindings and passes their snapshot to the worker.
func (p *Plan) ApplyWorker(job *batchv1.Job, workDir, taskID string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if p.TaskID != taskID || p.WorkDir != workDir {
		return fmt.Errorf("worker arguments differ from the resolved storage plan")
	}
	if job == nil || len(job.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("worker job requires a container")
	}
	if job.Namespace != "" && job.Namespace != p.Namespace {
		return fmt.Errorf("worker job namespace differs from storage plan")
	}
	if job.Name != taskID {
		return fmt.Errorf("worker Job name must match task ID %q", taskID)
	}
	containerIndex, err := selectContainer(job.Spec.Template.Spec.Containers, p.WorkerContainer, "WorkerContainer")
	if err != nil {
		return err
	}
	snapshot, err := p.Encode()
	if err != nil {
		return err
	}
	copy := job.DeepCopy()
	if copy.Labels == nil {
		copy.Labels = make(map[string]string)
	}
	copy.Labels["app"] = "funnel-worker"
	if len(p.OwnedVolumes) > 0 {
		copy.Finalizers = append(copy.Finalizers, Finalizer)
	}
	mounts := append([]corev1.VolumeMount{{Name: p.WorkspaceVolume, MountPath: path.Join(workDir, taskID), SubPath: taskID}}, p.WorkerMounts...)
	if err := addVolumesAndMounts(copy, p.Volumes, mounts, containerIndex); err != nil {
		return err
	}
	container := &copy.Spec.Template.Spec.Containers[containerIndex]
	for _, env := range container.Env {
		if env.Name == Env {
			return fmt.Errorf("worker job already configures %s", Env)
		}
	}
	container.Env = append(container.Env, corev1.EnvVar{Name: Env, Value: snapshot})
	if copy.Annotations == nil {
		copy.Annotations = make(map[string]string)
	}
	if _, exists := copy.Annotations[Annotation]; exists {
		return fmt.Errorf("worker job already has storage plan annotation")
	}
	copy.Annotations[Annotation] = snapshot
	*job = *copy
	return nil
}

// ApplyExecutor maps container paths into the task directory on the workspace.
func (p *Plan) ApplyExecutor(job *batchv1.Job, taskID string, mounts []corev1.VolumeMount) error {
	if err := p.validate(); err != nil {
		return err
	}
	if p.TaskID != taskID {
		return fmt.Errorf("executor task ID differs from storage plan")
	}
	if job == nil || len(job.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("executor job requires a container")
	}
	if job.Namespace != "" && job.Namespace != p.Namespace {
		return fmt.Errorf("executor job namespace differs from storage plan")
	}
	containerIndex, err := selectContainer(job.Spec.Template.Spec.Containers, p.ExecutorContainer, "ExecutorContainer")
	if err != nil {
		return err
	}
	var workspace corev1.Volume
	for _, v := range p.Volumes {
		if v.Name == p.WorkspaceVolume {
			workspace = *v.DeepCopy()
			break
		}
	}
	resolved := make([]corev1.VolumeMount, len(mounts))
	for i, mount := range mounts {
		if err := validateAbsolutePath(mount.MountPath); err != nil {
			return err
		}
		if mount.SubPath != "" || mount.SubPathExpr != "" {
			return fmt.Errorf("executor mounts must supply container paths without subPath overrides")
		}
		mount.Name = p.WorkspaceVolume
		mount.SubPath = path.Join(taskID, strings.TrimPrefix(mount.MountPath, "/"))
		resolved[i] = mount
	}
	copy := job.DeepCopy()
	if err := addVolumesAndMounts(copy, []corev1.Volume{workspace}, resolved, containerIndex); err != nil {
		return err
	}
	*job = *copy
	return nil
}

func addVolumesAndMounts(job *batchv1.Job, volumes []corev1.Volume, mounts []corev1.VolumeMount, containerIndex int) error {
	pod := &job.Spec.Template.Spec
	if containerIndex < 0 || containerIndex >= len(pod.Containers) {
		return fmt.Errorf("storage container index %d is out of range", containerIndex)
	}
	seenVolumes := make(map[string]bool)
	for _, v := range pod.Volumes {
		seenVolumes[v.Name] = true
	}
	for _, v := range volumes {
		if seenVolumes[v.Name] {
			return fmt.Errorf("storage volume %q conflicts with job template", v.Name)
		}
		seenVolumes[v.Name] = true
		pod.Volumes = append(pod.Volumes, *v.DeepCopy())
	}
	var seenPaths []string
	for _, m := range pod.Containers[containerIndex].VolumeMounts {
		seenPaths = append(seenPaths, m.MountPath)
	}
	for _, m := range mounts {
		if err := validateMount(m); err != nil {
			return err
		}
		for _, existing := range seenPaths {
			if pathsOverlap(existing, m.MountPath) {
				return fmt.Errorf("storage mount %q overlaps job mount %q", m.MountPath, existing)
			}
		}
		seenPaths = append(seenPaths, m.MountPath)
		pod.Containers[containerIndex].VolumeMounts = append(pod.Containers[containerIndex].VolumeMounts, *m.DeepCopy())
	}
	return nil
}
