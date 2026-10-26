// Package storageplan resolves native Kubernetes storage objects for one task.
package storageplan

import (
	"fmt"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	planVersion    = 4
	s3WriteTimeout = 3 * time.Hour
	Annotation     = "funnel.calypr.org/storage-plan"
	Env            = "FUNNEL_KUBERNETES_STORAGE_PLAN"
	Finalizer      = "funnel.calypr.org/storage-cleanup"
)

// Plan records the resolved mounts and the exact names of task-owned resources.
// Creation objects are excluded from the durable snapshot.
type Plan struct {
	Version           int
	TaskID            string
	Namespace         string
	WorkDir           string
	WorkspaceVolume   string
	WorkerContainer   string
	ExecutorContainer string
	S3ReadMounts      []storage.S3ReadMount  `json:"S3ReadMounts,omitempty"`
	S3WriteMounts     []storage.S3WriteMount `json:"S3WriteMounts,omitempty"`
	Volumes           []corev1.Volume
	WorkerMounts      []corev1.VolumeMount
	OwnedClaims       []string
	OwnedVolumes      []string
	Claims            []*corev1.PersistentVolumeClaim `json:"-"`
	PersistentVolumes []*corev1.PersistentVolume      `json:"-"`
}

// Ownership contains only the validated identity and resource names needed by cleanup.
type Ownership struct {
	TaskID          string
	Namespace       string
	WorkerContainer string
	OwnedVolumes    []string
}

// Resolve returns nil when Kubernetes storage and all S3 mounted routes are absent.
func Resolve(task *tes.Task, conf *config.Config) (*Plan, error) {
	if conf == nil {
		return nil, nil
	}
	if conf.Kubernetes == nil || conf.Kubernetes.Storage == nil {
		if hasS3MountedReads(conf.GenericS3) || hasS3MountedWrites(conf.GenericS3) {
			return nil, fmt.Errorf("S3 mounted reads or writes require Kubernetes.Storage")
		}
		return nil, nil
	}
	if task == nil || task.Id == "" {
		return nil, fmt.Errorf("storage requires a task ID")
	}
	ns := conf.Kubernetes.JobsNamespace
	if ns == "" {
		ns = conf.Kubernetes.Namespace
	}
	if errs := validation.IsDNS1123Label(ns); len(errs) != 0 {
		return nil, fmt.Errorf("invalid storage namespace %q: %s", ns, strings.Join(errs, ", "))
	}
	storageConfig := conf.Kubernetes.Storage
	data := map[string]interface{}{"TaskId": task.Id, "Namespace": ns, "DiskGb": task.GetResources().GetDiskGb(), "Bucket": "", "Region": "", "KmsKeyID": ""}
	if len(conf.GenericS3) > 0 && conf.GenericS3[0] != nil {
		data["Bucket"], data["Region"], data["KmsKeyID"] = conf.GenericS3[0].Bucket, conf.GenericS3[0].Region, conf.GenericS3[0].KmsKeyID
	}
	volumes, err := parseVolumes(storageConfig.Volumes, data)
	if err != nil {
		return nil, err
	}
	workerMounts, mountsByVolume, err := resolveWorkerMounts(storageConfig.VolumeMounts, data)
	if err != nil {
		return nil, err
	}
	// Validate mounted S3 identities before planning any resources so invalid
	// routes cannot reach the resource creation path.
	s3ReadMounts, err := resolveS3ReadMounts(conf.GenericS3, mountsByVolume)
	if err != nil {
		return nil, err
	}
	s3WriteMounts, err := resolveS3WriteMounts(conf.GenericS3, mountsByVolume)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Version:           planVersion,
		TaskID:            task.Id,
		Namespace:         ns,
		WorkDir:           conf.GetWorker().GetWorkDir(),
		WorkspaceVolume:   storageConfig.WorkspaceVolume,
		WorkerContainer:   storageConfig.WorkerContainer,
		ExecutorContainer: storageConfig.ExecutorContainer,
		S3ReadMounts:      s3ReadMounts,
		S3WriteMounts:     s3WriteMounts,
		Volumes:           volumes,
		WorkerMounts:      workerMounts,
	}
	claims, templateVolumes, err := parseClaimTemplates(storageConfig.VolumeClaimTemplates, data, ns, volumes, p.WorkspaceVolume, task.Id)
	if err != nil {
		return nil, err
	}
	p.Volumes = append(p.Volumes, templateVolumes...)
	p.Claims = claims
	for _, claim := range claims {
		p.OwnedClaims = append(p.OwnedClaims, claim.Name)
	}
	pvs, pvsByName, err := parsePersistentVolumeTemplates(storageConfig.PersistentVolumeTemplates, data, task.Id)
	if err != nil {
		return nil, err
	}
	p.PersistentVolumes = pvs
	for _, volume := range pvs {
		p.OwnedVolumes = append(p.OwnedVolumes, volume.Name)
	}
	if err := linkResources(p, pvsByName); err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	for _, claim := range p.Claims {
		if err := p.ValidateMountedWriteClaim(claim); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// ValidateMountedWriteClaim checks output access for a template or existing PVC
// against a resolved plan. Read-only input claims do not require write access.
func (p *Plan) ValidateMountedWriteClaim(claim *corev1.PersistentVolumeClaim) error {
	if len(p.S3WriteMounts) == 0 {
		return nil
	}
	for _, mode := range claim.Spec.AccessModes {
		switch mode {
		case corev1.ReadWriteOnce, corev1.ReadWriteMany, corev1.ReadWriteOncePod:
			return nil
		}
	}
	writeRoots := make(map[string]bool, len(p.S3WriteMounts))
	for _, mount := range p.S3WriteMounts {
		writeRoots[mount.Root] = true
	}
	writeVolumes := make(map[string]bool)
	for _, mount := range p.WorkerMounts {
		if writeRoots[mount.MountPath] {
			writeVolumes[mount.Name] = true
		}
	}
	for _, volume := range p.Volumes {
		if writeVolumes[volume.Name] && volume.PersistentVolumeClaim.ClaimName == claim.Name {
			return fmt.Errorf("S3 mounted write volume %q claim %s/%s must support a writable access mode: ReadWriteOnce, ReadWriteMany, or ReadWriteOncePod", volume.Name, claim.Namespace, claim.Name)
		}
	}
	return nil
}

func validName(name string) bool { return len(validation.IsDNS1123Subdomain(name)) == 0 }

func (p *Plan) validate() error {
	if p == nil {
		return fmt.Errorf("unsupported storage plan version")
	}
	if p.Version != planVersion {
		return fmt.Errorf("unsupported storage plan version %d; version %d is required", p.Version, planVersion)
	}
	if !validName(p.TaskID) {
		return fmt.Errorf("invalid storage task ID %q", p.TaskID)
	}
	if len(validation.IsDNS1123Label(p.Namespace)) != 0 {
		return fmt.Errorf("invalid storage namespace %q", p.Namespace)
	}
	if err := validateAbsolutePath(p.WorkDir); err != nil {
		return fmt.Errorf("storage work directory: %w", err)
	}
	volumes := make(map[string]corev1.Volume)
	claims := make(map[string]bool)
	for _, v := range p.Volumes {
		if len(validation.IsDNS1123Label(v.Name)) != 0 {
			return fmt.Errorf("invalid storage volume name %q", v.Name)
		}
		if _, exists := volumes[v.Name]; exists {
			return fmt.Errorf("duplicate storage volume %q", v.Name)
		}
		if v.PersistentVolumeClaim == nil || !validName(v.PersistentVolumeClaim.ClaimName) {
			return fmt.Errorf("storage volume %q must reference a valid PVC", v.Name)
		}
		expected := corev1.VolumeSource{PersistentVolumeClaim: v.PersistentVolumeClaim}
		if !reflect.DeepEqual(v.VolumeSource, expected) {
			return fmt.Errorf("storage volume %q has multiple sources", v.Name)
		}
		volumes[v.Name], claims[v.PersistentVolumeClaim.ClaimName] = v, true
	}
	workspace, exists := volumes[p.WorkspaceVolume]
	if !exists {
		return fmt.Errorf("workspace volume %q does not name a storage volume", p.WorkspaceVolume)
	}
	if workspace.PersistentVolumeClaim.ReadOnly {
		return fmt.Errorf("workspace volume must be writable")
	}
	for _, names := range [][]string{p.OwnedClaims, p.OwnedVolumes} {
		seen := make(map[string]bool)
		for _, name := range names {
			if !validName(name) || seen[name] || !strings.Contains(name, p.TaskID) {
				return fmt.Errorf("invalid or duplicate owned storage resource %q", name)
			}
			seen[name] = true
		}
	}
	if len(p.OwnedVolumes) > len(p.OwnedClaims) {
		return fmt.Errorf("owned persistent volumes require task-owned claims")
	}
	for _, claim := range p.OwnedClaims {
		if !claims[claim] {
			return fmt.Errorf("owned claim %q is not referenced by a storage volume", claim)
		}
	}
	paths := []string{path.Join(p.WorkDir, p.TaskID)}
	mountRoots := make(map[string]bool, len(p.WorkerMounts))
	for _, mount := range p.WorkerMounts {
		if _, ok := volumes[mount.Name]; !ok {
			return fmt.Errorf("worker mount references unknown volume %q", mount.Name)
		}
		if err := validateMount(mount); err != nil {
			return err
		}
		for _, existing := range paths {
			if pathsOverlap(existing, mount.MountPath) {
				return fmt.Errorf("worker mount %q overlaps mount %q", mount.MountPath, existing)
			}
		}
		paths = append(paths, mount.MountPath)
		mountRoots[mount.MountPath] = true
	}
	if err := storage.ValidateS3ReadMounts(p.S3ReadMounts); err != nil {
		return fmt.Errorf("invalid S3 read mounts: %w", err)
	}
	for _, readMount := range p.S3ReadMounts {
		if !mountRoots[readMount.Root] {
			return fmt.Errorf("S3 mounted read root %q does not match an explicit worker mount", readMount.Root)
		}
	}
	if err := storage.ValidateS3WriteMounts(p.S3WriteMounts); err != nil {
		return fmt.Errorf("invalid S3 write mounts: %w", err)
	}
	writableMountRoots := make(map[string]bool)
	for _, mount := range p.WorkerMounts {
		volume := volumes[mount.Name]
		if !mount.ReadOnly && volume.PersistentVolumeClaim != nil && !volume.PersistentVolumeClaim.ReadOnly {
			writableMountRoots[mount.MountPath] = true
		}
	}
	for _, writeMount := range p.S3WriteMounts {
		if !writableMountRoots[writeMount.Root] {
			return fmt.Errorf("S3 mounted write root %q must match an explicit writable worker mount", writeMount.Root)
		}
	}
	if err := validateMountedWriteRoutes(p.S3WriteMounts, p.WorkerMounts, volumes); err != nil {
		return err
	}
	return nil
}
