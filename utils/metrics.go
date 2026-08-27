package utils

import (
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

type ServerMetrics struct {
	Timestamp string        `json:"timestamp"`
	CPU       CPUMetrics    `json:"cpu"`
	Memory    MemoryMetrics `json:"memory"`
	Disk      DiskMetrics   `json:"disk"`
	Uptime    uint64        `json:"uptime_seconds"`
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

type DiskMetrics struct {
	TotalGB      uint64  `json:"total_gb"`
	UsedGB       uint64  `json:"used_gb"`
	FreeGB       uint64  `json:"free_gb"`
	UsagePercent float64 `json:"usage_percent"`
}

var metricsMutex sync.Mutex

var criticalState = struct {
	CPU    bool
	Memory bool
	Disk   bool
}{}

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

	diskUsage, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	uptime, err := host.Uptime()
	if err != nil {
		return nil, err
	}

	metrics := &ServerMetrics{
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

		Disk: DiskMetrics{
			TotalGB:      diskUsage.Total / 1024 / 1024 / 1024,
			UsedGB:       diskUsage.Used / 1024 / 1024 / 1024,
			FreeGB:       diskUsage.Free / 1024 / 1024 / 1024,
			UsagePercent: diskUsage.UsedPercent,
		},

		Uptime: uptime,
	}

	checkCriticalStates(metrics)

	return metrics, nil
}

func checkCriticalStates(metrics *ServerMetrics) {
	metricsMutex.Lock()
	defer metricsMutex.Unlock()

	checkMetric(
		"CPU",
		metrics.CPU.UsagePercent,
		90,
		&criticalState.CPU,
	)

	checkMetric(
		"RAM",
		metrics.Memory.UsagePercent,
		90,
		&criticalState.Memory,
	)

	checkMetric(
		"DISK",
		metrics.Disk.UsagePercent,
		90,
		&criticalState.Disk,
	)
}

func checkMetric(name string, value float64, threshold float64, state *bool) {
	if value >= threshold {
		if !*state {
			*state = true

			_ = WriteLog(
				"CRITICAL",
				fmt.Sprintf("HIGH_%s", name),
				fmt.Sprintf("%s superó el %.0f%%: %.2f%%", name, threshold, value),
			)
		}

		return
	}

	if *state {
		*state = false

		_ = WriteLog(
			"INFO",
			fmt.Sprintf("%s_RECOVERED", name),
			fmt.Sprintf("%s volvió a niveles normales: %.2f%%", name, value),
		)
	}
}


func StartMetricsMonitor() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C

			_, err := GetServerMetrics()
			if err != nil {
				_ = WriteLog(
					"CRITICAL",
					"METRICS_ERROR",
					"Error obteniendo métricas del servidor: "+err.Error(),
				)
			}
		}
	}()
}