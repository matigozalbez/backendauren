package utils

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type ServerMetrics struct {
	Timestamp string        `json:"timestamp"`
	CPU       CPUMetrics    `json:"cpu"`
	Memory    MemoryMetrics `json:"memory"`
}

type CPUMetrics struct {
	UsagePercent float64 `json:"usage_percent"`
	Cores        int     `json:"cores"`
}

type MemoryMetrics struct {
	TotalMB      uint64  `json:"total_mb"`
	UsedMB       uint64  `json:"used_mb"`
	AvailableMB  uint64  `json:"available_mb"`
	UsagePercent float64 `json:"usage_percent"`
}

func GetServerMetrics() (*ServerMetrics, error) {

	cpuPercent, err := cpu.Percent(time.Second, false)
	if err != nil {
		return nil, err
	}

	cpuUsage := 0.0
	if len(cpuPercent) > 0 {
		cpuUsage = cpuPercent[0]
	}

	cpuCores, err := cpu.Counts(true)
	if err != nil {
		return nil, err
	}

	memory, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	return &ServerMetrics{
		Timestamp: time.Now().UTC().Format(time.RFC3339),

		CPU: CPUMetrics{
			UsagePercent: cpuUsage,
			Cores:        cpuCores,
		},

		Memory: MemoryMetrics{
			TotalMB:      memory.Total / 1024 / 1024,
			UsedMB:       memory.Used / 1024 / 1024,
			AvailableMB:  memory.Available / 1024 / 1024,
			UsagePercent: memory.UsedPercent,
		},
	}, nil
}