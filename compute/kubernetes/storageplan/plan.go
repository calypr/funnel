// Package storageplan resolves native Kubernetes storage objects for one task.
package storageplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"strings"
	"text/template"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/types/known/structpb"
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
	S3ReadMounts      []storage.S3ReadMount
	S3WriteMounts     []storage.S3WriteMount
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

func parseVolumes(rawVolumes []*structpb.Struct, data map[string]interface{}) ([]corev1.Volume, error) {
	volumes := make([]corev1.Volume, 0, len(rawVolumes))
	seen := make(map[string]bool, len(rawVolumes))
	for i, raw := range rawVolumes {
		volume := corev1.Volume{}
		if err := decodeStruct(raw, data, &volume); err != nil {
			return nil, fmt.Errorf("Kubernetes.Storage.Volumes[%d]: %w", i, err)
		}
		if len(validation.IsDNS1123Label(volume.Name)) != 0 {
			return nil, fmt.Errorf("Kubernetes.Storage.Volumes[%d] has invalid name %q", i, volume.Name)
		}
		if seen[volume.Name] {
			return nil, fmt.Errorf("duplicate storage volume %q", volume.Name)
		}
		seen[volume.Name] = true
		if volume.PersistentVolumeClaim == nil || !validName(volume.PersistentVolumeClaim.ClaimName) {
			return nil, fmt.Errorf("storage volume %q must reference a valid PVC", volume.Name)
		}
		expected := corev1.VolumeSource{PersistentVolumeClaim: volume.PersistentVolumeClaim}
		if !reflect.DeepEqual(volume.VolumeSource, expected) {
			return nil, fmt.Errorf("storage volume %q must have exactly one PVC source", volume.Name)
		}
		volumes = append(volumes, volume)
	}
	return volumes, nil
}

func resolveWorkerMounts(rawMounts []*structpb.Struct, data map[string]interface{}) ([]corev1.VolumeMount, map[string][]corev1.VolumeMount, error) {
	workerMounts := make([]corev1.VolumeMount, 0, len(rawMounts))
	mountsByVolume := make(map[string][]corev1.VolumeMount)
	for i, raw := range rawMounts {
		mount := corev1.VolumeMount{}
		if err := decodeStruct(raw, data, &mount); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeMounts[%d]: %w", i, err)
		}
		if len(validation.IsDNS1123Label(mount.Name)) != 0 {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeMounts[%d] has invalid volume name %q", i, mount.Name)
		}
		if err := validateMount(mount); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeMounts[%d]: %w", i, err)
		}
		mountsByVolume[mount.Name] = append(mountsByVolume[mount.Name], mount)
		workerMounts = append(workerMounts, mount)
	}
	return workerMounts, mountsByVolume, nil
}

func parseClaimTemplates(rawClaims []*structpb.Struct, data map[string]interface{}, namespace string, volumes []corev1.Volume, workspaceVolume, taskID string) ([]*corev1.PersistentVolumeClaim, []corev1.Volume, error) {
	claims := make([]*corev1.PersistentVolumeClaim, 0, len(rawClaims))
	templateVolumes := make([]corev1.Volume, 0, len(rawClaims))
	explicitVolumes := make(map[string]bool, len(volumes))
	externalClaims := make(map[string]bool, len(volumes))
	logicalNames := make(map[string]bool, len(rawClaims))
	for _, volume := range volumes {
		explicitVolumes[volume.Name] = true
		externalClaims[volume.PersistentVolumeClaim.ClaimName] = true
	}
	for i, raw := range rawClaims {
		if raw == nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeClaimTemplates[%d]: native object must not be null", i)
		}
		metadata, ok := raw.AsMap()["metadata"].(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("PVC template %d requires metadata.name as a static DNS1123 label", i)
		}
		logicalName, ok := metadata["name"].(string)
		if !ok || strings.Contains(logicalName, "{{") || strings.Contains(logicalName, "}}") || len(validation.IsDNS1123Label(logicalName)) != 0 {
			return nil, nil, fmt.Errorf("PVC template %d metadata.name must be a static DNS1123 label; remove task templating because claim names are generated from the task ID", i)
		}
		if logicalNames[logicalName] {
			return nil, nil, fmt.Errorf("duplicate PVC template logical name %q", logicalName)
		}
		logicalNames[logicalName] = true
		if explicitVolumes[logicalName] {
			return nil, nil, fmt.Errorf("PVC template logical name %q collides with an explicit storage volume", logicalName)
		}
		claim := &corev1.PersistentVolumeClaim{}
		if err := decodeStruct(raw, data, claim); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeClaimTemplates[%d]: %w", i, err)
		}
		if claim.APIVersion == "" {
			claim.APIVersion = "v1"
		}
		if claim.Kind == "" {
			claim.Kind = "PersistentVolumeClaim"
		}
		if claim.APIVersion != "v1" || claim.Kind != "PersistentVolumeClaim" {
			return nil, nil, fmt.Errorf("PVC template %d must be a v1 PersistentVolumeClaim", i)
		}
		if claim.Namespace == "" {
			claim.Namespace = namespace
		}
		if claim.Namespace != namespace {
			return nil, nil, fmt.Errorf("PVC template %q namespace must be %q", logicalName, namespace)
		}
		if len(claim.OwnerReferences) != 0 {
			return nil, nil, fmt.Errorf("PVC template %q must not contain owner references", logicalName)
		}
		if claim.Spec.VolumeMode != nil && *claim.Spec.VolumeMode == corev1.PersistentVolumeBlock {
			return nil, nil, fmt.Errorf("PVC template %q must use filesystem volume mode", logicalName)
		}
		if claim.GenerateName != "" {
			return nil, nil, fmt.Errorf("PVC template %q must not contain generateName", logicalName)
		}
		claim.Name = taskID + "-" + logicalName
		if !validName(claim.Name) {
			return nil, nil, fmt.Errorf("generated PVC name %q is invalid or exceeds the Kubernetes DNS subdomain limit", claim.Name)
		}
		if externalClaims[claim.Name] {
			return nil, nil, fmt.Errorf("explicit storage volume references generated task-owned PVC %q; use logical volume name %q", claim.Name, logicalName)
		}
		if claim.Spec.VolumeName != "" && !validName(claim.Spec.VolumeName) {
			return nil, nil, fmt.Errorf("PVC template %q has invalid volumeName %q", logicalName, claim.Spec.VolumeName)
		}
		if logicalName == workspaceVolume && len(claim.Spec.AccessModes) > 0 {
			writable := false
			for _, mode := range claim.Spec.AccessModes {
				if mode == corev1.ReadWriteOncePod {
					return nil, nil, fmt.Errorf("workspace PVC cannot use ReadWriteOncePod: worker and executor run in separate Pods")
				}
				if mode == corev1.ReadWriteOnce || mode == corev1.ReadWriteMany {
					writable = true
				}
			}
			if !writable {
				return nil, nil, fmt.Errorf("workspace PVC must support writable access from worker and executor Pods")
			}
		}
		claims = append(claims, claim)
		templateVolumes = append(templateVolumes, corev1.Volume{
			Name: logicalName,
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: claim.Name,
			}},
		})
	}
	return claims, templateVolumes, nil
}

func parsePersistentVolumeTemplates(rawVolumes []*structpb.Struct, data map[string]interface{}, taskID string) ([]*corev1.PersistentVolume, map[string]*corev1.PersistentVolume, error) {
	volumes := make([]*corev1.PersistentVolume, 0, len(rawVolumes))
	byName := make(map[string]*corev1.PersistentVolume, len(rawVolumes))
	for i, raw := range rawVolumes {
		volume := &corev1.PersistentVolume{}
		if err := decodeStruct(raw, data, volume); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.PersistentVolumeTemplates[%d]: %w", i, err)
		}
		if volume.APIVersion != "v1" || volume.Kind != "PersistentVolume" {
			return nil, nil, fmt.Errorf("PV template %d must be a v1 PersistentVolume", i)
		}
		if volume.Namespace != "" || volume.GenerateName != "" || !validName(volume.Name) || !strings.Contains(volume.Name, taskID) {
			return nil, nil, fmt.Errorf("PV template %d requires a cluster-scoped explicit task-specific name", i)
		}
		if len(volume.OwnerReferences) != 0 {
			return nil, nil, fmt.Errorf("PV %q must not contain owner references", volume.Name)
		}
		if volume.Spec.VolumeMode != nil && *volume.Spec.VolumeMode == corev1.PersistentVolumeBlock {
			return nil, nil, fmt.Errorf("PV %q must use filesystem volume mode", volume.Name)
		}
		if persistentVolumeSourceCount(volume.Spec.PersistentVolumeSource) != 1 {
			return nil, nil, fmt.Errorf("PV %q must have exactly one native volume source", volume.Name)
		}
		if _, exists := byName[volume.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate PV template name %q", volume.Name)
		}
		byName[volume.Name] = volume
		volumes = append(volumes, volume)
	}
	return volumes, byName, nil
}

func persistentVolumeSourceCount(source corev1.PersistentVolumeSource) int {
	value := reflect.ValueOf(source)
	count := 0
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Pointer && !field.IsNil() {
			count++
		}
	}
	return count
}

func linkResources(plan *Plan, volumes map[string]*corev1.PersistentVolume) error {
	linkedPVClaims := make(map[string]string, len(volumes))
	for _, claim := range plan.Claims {
		pv, managedPV := volumes[claim.Spec.VolumeName]
		if !managedPV {
			continue
		}
		if previous := linkedPVClaims[pv.Name]; previous != "" {
			return fmt.Errorf("PV template %q is referenced by multiple PVC templates %q and %q", pv.Name, previous, claim.Name)
		}
		linkedPVClaims[pv.Name] = claim.Name
		if pv.Spec.ClaimRef == nil {
			pv.Spec.ClaimRef = &corev1.ObjectReference{}
		}
		ref := pv.Spec.ClaimRef
		if (ref.Name != "" && ref.Name != claim.Name) || (ref.Namespace != "" && ref.Namespace != claim.Namespace) || ref.UID != "" || (ref.Kind != "" && ref.Kind != "PersistentVolumeClaim") || (ref.APIVersion != "" && ref.APIVersion != "v1") {
			return fmt.Errorf("PV %q claimRef conflicts with PVC %q", pv.Name, claim.Name)
		}
		ref.Name, ref.Namespace = claim.Name, claim.Namespace
	}
	for name := range volumes {
		if linkedPVClaims[name] == "" {
			return fmt.Errorf("PV template %q is not referenced by exactly one PVC template", name)
		}
	}
	return nil
}

func decodeStruct(raw *structpb.Struct, data map[string]interface{}, target interface{}) error {
	if raw == nil {
		return fmt.Errorf("native object must not be null")
	}
	value, err := renderStringValues(raw.AsMap(), data)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func renderStringValues(value interface{}, data map[string]interface{}) (interface{}, error) {
	switch value := value.(type) {
	case string:
		t, err := template.New("storage-string").Option("missingkey=error").Parse(value)
		if err != nil {
			return nil, err
		}
		var rendered bytes.Buffer
		if err := t.Execute(&rendered, data); err != nil {
			return nil, err
		}
		return rendered.String(), nil
	case []interface{}:
		for i, item := range value {
			rendered, err := renderStringValues(item, data)
			if err != nil {
				return nil, err
			}
			value[i] = rendered
		}
		return value, nil
	case map[string]interface{}:
		for key, item := range value {
			rendered, err := renderStringValues(item, data)
			if err != nil {
				return nil, err
			}
			value[key] = rendered
		}
		return value, nil
	default:
		return value, nil
	}
}

func hasS3MountedReads(backends []*config.GenericS3Storage) bool {
	for _, backend := range backends {
		if backend != nil && backend.MountedRead != nil {
			return true
		}
	}
	return false
}

func hasS3MountedWrites(backends []*config.GenericS3Storage) bool {
	for _, backend := range backends {
		if backend != nil && backend.MountedWrite != nil {
			return true
		}
	}
	return false
}

func resolveS3ReadMounts(backends []*config.GenericS3Storage, mountsByVolume map[string][]corev1.VolumeMount) ([]storage.S3ReadMount, error) {
	readMounts := make([]storage.S3ReadMount, 0)
	for _, backend := range backends {
		if backend == nil || backend.MountedRead == nil {
			continue
		}
		if !backend.Valid() {
			return nil, fmt.Errorf("S3 mounted read backend %q is disabled or invalid", backend.Bucket)
		}
		if backend.Bucket == "" {
			return nil, fmt.Errorf("S3 mounted read requires a bucket")
		}
		if matches := matchingS3Backends(backends, backend); matches > 1 {
			return nil, fmt.Errorf("duplicate mounted S3 backend %q bucket %q", backend.Endpoint, backend.Bucket)
		}
		var subPath *string
		if backend.MountedRead.MountView != nil {
			value := backend.MountedRead.GetSubPath()
			subPath = &value
		}
		mount, err := selectS3Mount(backend.MountedRead.Volume, subPath, mountsByVolume, "read")
		if err != nil {
			return nil, err
		}
		readMounts = append(readMounts, storage.S3ReadMount{
			Endpoint: backend.Endpoint,
			Bucket:   backend.Bucket,
			Prefix:   backend.MountedRead.Prefix,
			Root:     mount.MountPath,
		})
	}
	if err := storage.ValidateS3ReadMounts(readMounts); err != nil {
		return nil, fmt.Errorf("GenericS3 mounted reads: %w", err)
	}
	return readMounts, nil
}

func resolveS3WriteMounts(backends []*config.GenericS3Storage, mountsByVolume map[string][]corev1.VolumeMount) ([]storage.S3WriteMount, error) {
	writeMounts := make([]storage.S3WriteMount, 0)
	for _, backend := range backends {
		if backend == nil || backend.MountedWrite == nil {
			continue
		}
		if !backend.Valid() {
			return nil, fmt.Errorf("S3 mounted write backend %q is disabled or invalid", backend.Bucket)
		}
		if backend.Bucket == "" {
			return nil, fmt.Errorf("S3 mounted write requires a bucket")
		}
		if matches := matchingS3Backends(backends, backend); matches > 1 {
			return nil, fmt.Errorf("duplicate mounted S3 backend %q bucket %q", backend.Endpoint, backend.Bucket)
		}
		var subPath *string
		if backend.MountedWrite.MountView != nil {
			value := backend.MountedWrite.GetSubPath()
			subPath = &value
		}
		mount, err := selectS3Mount(backend.MountedWrite.Volume, subPath, mountsByVolume, "write")
		if err != nil {
			return nil, err
		}
		if mount.ReadOnly {
			return nil, fmt.Errorf("S3 mounted write volume %q mount %q must be writable", backend.MountedWrite.Volume, mount.MountPath)
		}
		timeout := s3WriteTimeout
		if configured := backend.MountedWrite.Timeout; configured != nil {
			if err := configured.CheckValid(); err != nil {
				return nil, fmt.Errorf("S3 mounted write timeout: %w", err)
			}
			timeout = configured.AsDuration()
			if timeout <= 0 {
				return nil, fmt.Errorf("S3 mounted write timeout must be positive")
			}
		}
		writeMounts = append(writeMounts, storage.S3WriteMount{
			Endpoint: backend.Endpoint,
			Bucket:   backend.Bucket,
			Prefix:   backend.MountedWrite.Prefix,
			Root:     mount.MountPath,
			Timeout:  timeout,
		})
	}
	if err := storage.ValidateS3WriteMounts(writeMounts); err != nil {
		return nil, fmt.Errorf("GenericS3 mounted writes: %w", err)
	}
	return writeMounts, nil
}

func matchingS3Backends(backends []*config.GenericS3Storage, backend *config.GenericS3Storage) int {
	matches := 0
	for _, candidate := range backends {
		if candidate != nil && candidate.Valid() && candidate.Endpoint == backend.Endpoint && candidate.Bucket == backend.Bucket {
			matches++
		}
	}
	return matches
}

func selectS3Mount(volume string, subPath *string, mountsByVolume map[string][]corev1.VolumeMount, direction string) (corev1.VolumeMount, error) {
	if len(validation.IsDNS1123Label(volume)) != 0 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s has invalid volume reference %q", direction, volume)
	}
	candidates := mountsByVolume[volume]
	if len(candidates) == 0 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s references unknown worker volume %q", direction, volume)
	}
	if subPath == nil {
		if len(candidates) != 1 {
			return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s volume %q has %d worker mounts; specify SubPath", direction, volume, len(candidates))
		}
		return candidates[0], nil
	}
	var selected corev1.VolumeMount
	matches := 0
	for _, candidate := range candidates {
		if candidate.SubPath == *subPath && candidate.SubPathExpr == "" {
			selected = candidate
			matches++
		}
	}
	if matches == 0 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s volume %q has no static mount with subPath %q", direction, volume, *subPath)
	}
	if matches > 1 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s volume %q has multiple mounts with subPath %q", direction, volume, *subPath)
	}
	return selected, nil
}

func validName(name string) bool { return len(validation.IsDNS1123Subdomain(name)) == 0 }

func (p *Plan) Encode() (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	return string(data), err
}

func Decode(snapshot string) (*Plan, error) {
	p, err := decodeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// DecodeOwnership validates the storage identity used by cleanup while excluding
// S3 routes, which describe execution behavior and do not affect resource ownership.
func DecodeOwnership(snapshot string) (*Ownership, error) {
	p, err := decodeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	ownershipPlan := *p
	ownershipPlan.S3ReadMounts = nil
	ownershipPlan.S3WriteMounts = nil
	if err := ownershipPlan.validate(); err != nil {
		return nil, err
	}
	return &Ownership{
		TaskID:          p.TaskID,
		Namespace:       p.Namespace,
		WorkerContainer: p.WorkerContainer,
		OwnedVolumes:    append([]string(nil), p.OwnedVolumes...),
	}, nil
}

func decodeSnapshot(snapshot string) (*Plan, error) {
	var header struct{ Version int }
	if err := json.Unmarshal([]byte(snapshot), &header); err != nil {
		return nil, fmt.Errorf("decode storage plan version: %w", err)
	}
	if header.Version != planVersion {
		return nil, fmt.Errorf("unsupported storage plan version %d; version %d is required", header.Version, planVersion)
	}
	var p Plan
	decoder := json.NewDecoder(strings.NewReader(snapshot))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode storage plan: %w", err)
	}
	return &p, nil
}

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
