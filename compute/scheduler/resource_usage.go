package scheduler

import (
	"fmt"
	"math"

	"github.com/calypr/funnel/tes"
)

// ResourceUsage is a reservation snapshot or the sum of active reservations.
// Unlike available capacity it can exceed a node's reduced capacity.
type ResourceUsage struct {
	CPUs uint64  `json:"cpus"`
	RAM  float64 `json:"ram_gb"`
	Disk float64 `json:"disk_gb"`
}

func TaskResourceUsage(task *tes.Task) (ResourceUsage, error) {
	r := task.GetResources()
	if r.GetCpuCores() < 0 || !validUsage(r.GetRamGb()) || !validUsage(r.GetDiskGb()) {
		return ResourceUsage{}, fmt.Errorf("invalid task resource reservation")
	}
	return ResourceUsage{CPUs: uint64(r.GetCpuCores()), RAM: r.GetRamGb(), Disk: r.GetDiskGb()}, nil
}
func ValidateNodeCapacity(total *Resources) error {
	if !validUsage(total.GetRamGb()) || !validUsage(total.GetDiskGb()) {
		return fmt.Errorf("node capacity must be finite and nonnegative")
	}
	return nil
}
func validUsage(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
func (u ResourceUsage) Add(v ResourceUsage) (ResourceUsage, error) {
	sum := ResourceUsage{CPUs: u.CPUs + v.CPUs, RAM: u.RAM + v.RAM, Disk: u.Disk + v.Disk}
	if sum.CPUs < u.CPUs || !validUsage(sum.RAM) || !validUsage(sum.Disk) {
		return ResourceUsage{}, fmt.Errorf("resource reservation overflow")
	}
	return sum, nil
}
func (u ResourceUsage) Release(v ResourceUsage) (ResourceUsage, error) {
	// Floating-point GB totals can differ by rounding after repeated additions.
	tolerance := func(a, b float64) float64 { return 1e-12 * math.Max(1, math.Max(a, b)) }
	if u.CPUs < v.CPUs || u.RAM+tolerance(u.RAM, v.RAM) < v.RAM || u.Disk+tolerance(u.Disk, v.Disk) < v.Disk {
		return ResourceUsage{}, fmt.Errorf("resource reservation underflow")
	}
	return ResourceUsage{CPUs: u.CPUs - v.CPUs, RAM: math.Max(0, u.RAM-v.RAM), Disk: math.Max(0, u.Disk-v.Disk)}, nil
}
func (u ResourceUsage) Available(total *Resources) *Resources {
	var cpu uint32
	if uint64(total.GetCpus()) > u.CPUs {
		cpu = total.GetCpus() - uint32(u.CPUs)
	}
	return &Resources{Cpus: cpu, RamGb: math.Max(0, total.GetRamGb()-u.RAM), DiskGb: math.Max(0, total.GetDiskGb()-u.Disk)}
}
