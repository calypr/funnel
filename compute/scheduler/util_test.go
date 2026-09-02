package scheduler

import (
	"errors"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	pscpu "github.com/shirou/gopsutil/v4/cpu"
	psdisk "github.com/shirou/gopsutil/v4/disk"
	psmem "github.com/shirou/gopsutil/v4/mem"
)

func TestDetectResourcesSkipsConfiguredValues(t *testing.T) {
	probe := resourceProbe{
		cpuInfo: func() ([]pscpu.InfoStat, error) {
			t.Fatal("CPU probe should not run for a configured value")
			return nil, nil
		},
		virtualMemory: func() (*psmem.VirtualMemoryStat, error) {
			t.Fatal("memory probe should not run for a configured value")
			return nil, nil
		},
		diskUsage: func(string) (*psdisk.UsageStat, error) {
			t.Fatal("disk probe should not run for a configured value")
			return nil, nil
		},
	}
	conf := &config.Node{Resources: &config.Resources{Cpus: 4, RamGb: 8, DiskGb: 12}}

	got, err := detectResourcesWithProbe(conf, "/unused", probe)
	if err != nil {
		t.Fatal("unexpected detection error:", err)
	}
	if got.Cpus != 4 || got.RamGb != 8 || got.DiskGb != 12 {
		t.Fatalf("configured resources changed: %+v", got)
	}
}

func TestDetectResourcesContinuesAfterProbeFailure(t *testing.T) {
	cpuErr := errors.New("CPU unavailable")
	probe := resourceProbe{
		cpuInfo: func() ([]pscpu.InfoStat, error) {
			return nil, cpuErr
		},
		virtualMemory: func() (*psmem.VirtualMemoryStat, error) {
			return &psmem.VirtualMemoryStat{Total: 2_000_000_000}, nil
		},
		diskUsage: func(workdir string) (*psdisk.UsageStat, error) {
			if workdir != "/work" {
				t.Fatalf("unexpected workdir %q", workdir)
			}
			return &psdisk.UsageStat{Free: 3_000_000_000}, nil
		},
	}
	conf := &config.Node{Resources: &config.Resources{}}

	got, err := detectResourcesWithProbe(conf, "/work", probe)
	if !errors.Is(err, cpuErr) {
		t.Fatalf("expected CPU error, got %v", err)
	}
	if got.Cpus != 0 || got.RamGb != 2 || got.DiskGb != 3 {
		t.Fatalf("expected successful probes to be retained, got %+v", got)
	}
}

func TestDetectResourcesJoinsProbeFailures(t *testing.T) {
	memoryErr := errors.New("memory unavailable")
	diskErr := errors.New("disk unavailable")
	probe := resourceProbe{
		cpuInfo: func() ([]pscpu.InfoStat, error) {
			return []pscpu.InfoStat{{Cores: 10}}, nil
		},
		virtualMemory: func() (*psmem.VirtualMemoryStat, error) {
			return nil, memoryErr
		},
		diskUsage: func(string) (*psdisk.UsageStat, error) {
			return nil, diskErr
		},
	}
	conf := &config.Node{Resources: &config.Resources{}}

	got, err := detectResourcesWithProbe(conf, "/work", probe)
	if !errors.Is(err, memoryErr) || !errors.Is(err, diskErr) {
		t.Fatalf("expected joined memory and disk errors, got %v", err)
	}
	if got.Cpus != 10 {
		t.Fatalf("expected successful CPU detection to be retained, got %+v", got)
	}
}
