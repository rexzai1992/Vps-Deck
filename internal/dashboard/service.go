package dashboard

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

type Summary struct {
	CPUPercent    float64
	MemoryUsed    uint64
	MemoryTotal   uint64
	MemoryPercent float64
	DiskUsed      uint64
	DiskTotal     uint64
	DiskPercent   float64
	Uptime        time.Duration
	OS            string
	Kernel        string
	Architecture  string
	LocalIP       string
	ProjectCounts database.ProjectCounts
	CollectedAt   time.Time
}

type Service struct {
	db *database.DB
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Collect(ctx context.Context) (Summary, error) {
	var summary Summary

	percentages, err := cpu.PercentWithContext(ctx, 150*time.Millisecond, false)
	if err != nil {
		return summary, fmt.Errorf("read CPU usage: %w", err)
	}
	if len(percentages) > 0 {
		summary.CPUPercent = percentages[0]
	}

	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return summary, fmt.Errorf("read memory usage: %w", err)
	}
	summary.MemoryUsed = memory.Used
	summary.MemoryTotal = memory.Total
	summary.MemoryPercent = memory.UsedPercent

	storage, err := disk.UsageWithContext(ctx, "/")
	if err != nil {
		return summary, fmt.Errorf("read disk usage: %w", err)
	}
	summary.DiskUsed = storage.Used
	summary.DiskTotal = storage.Total
	summary.DiskPercent = storage.UsedPercent

	hostInfo, err := host.InfoWithContext(ctx)
	if err != nil {
		return summary, fmt.Errorf("read host information: %w", err)
	}
	summary.Uptime = time.Duration(hostInfo.Uptime) * time.Second
	summary.OS = strings.TrimSpace(hostInfo.Platform + " " + hostInfo.PlatformVersion)
	summary.Kernel = hostInfo.KernelVersion
	summary.Architecture = runtime.GOARCH
	summary.LocalIP = localIP()
	summary.CollectedAt = time.Now()

	summary.ProjectCounts, err = s.db.ProjectCounts(ctx)
	if err != nil {
		return summary, fmt.Errorf("count projects: %w", err)
	}
	return summary, nil
}

func localIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "Unavailable"
	}
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsLoopback() {
				return ip.String()
			}
		}
	}
	return "Unavailable"
}
