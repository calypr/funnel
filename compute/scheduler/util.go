package scheduler

import (
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/ohsu-comp-bio/funnel/config"
	pscpu "github.com/shirou/gopsutil/v4/cpu"
	psdisk "github.com/shirou/gopsutil/v4/disk"
	psmem "github.com/shirou/gopsutil/v4/mem"
)

type resourceProbe struct {
	cpuInfo       func() ([]pscpu.InfoStat, error)
	virtualMemory func() (*psmem.VirtualMemoryStat, error)
	diskUsage     func(string) (*psdisk.UsageStat, error)
}

var hostResourceProbe = resourceProbe{
	cpuInfo:       pscpu.Info,
	virtualMemory: psmem.VirtualMemory,
	diskUsage:     psdisk.Usage,
}

// GenNodeID returns a UUID string.
func GenNodeID() string {
	u, _ := uuid.NewV7()
	return u.String()
}

// detectResources helps determine the amount of resources to report.
// Resources are determined by inspecting the host, but they
// can be overridden by config.
//
// Upon error, detectResources will return the resources given by the config
// with the error.
func detectResources(conf *config.Node, workdir string) (*Resources, error) {
	return detectResourcesWithProbe(conf, workdir, hostResourceProbe)
}

func detectResourcesWithProbe(conf *config.Node, workdir string, probe resourceProbe) (*Resources, error) {
	res := &Resources{
		Cpus:   conf.Resources.Cpus,
		RamGb:  conf.Resources.RamGb,
		DiskGb: conf.Resources.DiskGb,
	}

	var detectionErrors []error

	if conf.Resources.Cpus == 0 {
		cpuinfo, err := probe.cpuInfo()
		if err != nil {
			detectionErrors = append(detectionErrors, fmt.Errorf("detecting CPU cores: %w", err))
		} else {
			// TODO is cores the best metric? with hyperthreading,
			//      runtime.NumCPU() and pscpu.Counts() return 8
			//      on my 4-core mac laptop
			for _, cpu := range cpuinfo {
				res.Cpus += uint32(cpu.Cores)
			}
		}
	}

	gb := math.Pow(1000, 3)
	if conf.Resources.RamGb == 0.0 {
		vmeminfo, err := probe.virtualMemory()
		if err != nil {
			detectionErrors = append(detectionErrors, fmt.Errorf("detecting memory: %w", err))
		} else {
			res.RamGb = float64(vmeminfo.Total) / gb
		}
	}

	if conf.Resources.DiskGb == 0.0 {
		diskinfo, err := probe.diskUsage(workdir)
		if err != nil {
			detectionErrors = append(detectionErrors, fmt.Errorf("detecting available disk: %w", err))
		} else {
			res.DiskGb = float64(diskinfo.Free) / gb
		}
	}

	return res, errors.Join(detectionErrors...)
}
