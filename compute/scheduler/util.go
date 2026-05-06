package scheduler

import (
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/ohsu-comp-bio/funnel/config"
	pscpu "github.com/shirou/gopsutil/cpu"
	psdisk "github.com/shirou/gopsutil/disk"
	psmem "github.com/shirou/gopsutil/mem"
)

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
	res := &Resources{
		Cpus:   conf.Resources.Cpus,
		RamGb:  conf.Resources.RamGb,
		DiskGb: conf.Resources.DiskGb,
	}

	cpuinfo, err := pscpu.Info()
	if err != nil {
		return res, fmt.Errorf("Error detecting cpu cores: %s", err)
	}
	vmeminfo, err := psmem.VirtualMemory()
	if err != nil {
		return res, fmt.Errorf("Error detecting memory: %s", err)
	}
	diskinfo, err := psdisk.Usage(workdir)
	if err != nil {
		return res, fmt.Errorf("Error detecting available disk: %s", err)
	}

	if conf.Resources.Cpus == 0 {
		// TODO is cores the best metric? with hyperthreading,
		//      runtime.NumCPU() and pscpu.Counts() return 8
		//      on my 4-core mac laptop
		for _, cpu := range cpuinfo {
			res.Cpus += uint32(cpu.Cores)
		}
	}

	gb := math.Pow(1000, 3)
	if conf.Resources.RamGb == 0.0 {
		res.RamGb = float64(vmeminfo.Total) / float64(gb)
	}

	if conf.Resources.DiskGb == 0.0 {
		res.DiskGb = float64(diskinfo.Free) / float64(gb)
	}

	return res, nil
}
