package monitoring

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

type PortProject struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type PortListener struct {
	Protocol    string        `json:"protocol"`
	Address     string        `json:"address"`
	Port        uint32        `json:"port"`
	Exposure    string        `json:"exposure"`
	ExposureKey string        `json:"exposure_key"`
	PID         int32         `json:"pid,omitempty"`
	Process     string        `json:"process,omitempty"`
	Projects    []PortProject `json:"projects,omitempty"`
}

type MissingProjectPort struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

type PortSnapshot struct {
	Enabled             bool                 `json:"enabled"`
	Status              string               `json:"status"`
	TCPCount            int                  `json:"tcp_count"`
	UDPCount            int                  `json:"udp_count"`
	TotalCount          int                  `json:"total_count"`
	Listeners           []PortListener       `json:"listeners"`
	MissingProjectPorts []MissingProjectPort `json:"missing_project_ports"`
	Warning             string               `json:"warning,omitempty"`
	CollectedAt         time.Time            `json:"collected_at"`
}

type connectionSource interface {
	Connections(context.Context, string) ([]gnet.ConnectionStat, error)
}

type gopsutilConnectionSource struct{}

func (gopsutilConnectionSource) Connections(ctx context.Context, kind string) ([]gnet.ConnectionStat, error) {
	return gnet.ConnectionsWithContext(ctx, kind)
}

type processLookup func(context.Context, int32) string

type PortService struct {
	db          *database.DB
	enabled     bool
	ttl         time.Duration
	source      connectionSource
	processName processLookup
	cache       snapshotCache[PortSnapshot]
}

func NewPortService(db *database.DB, enabled bool, ttl time.Duration) *PortService {
	return &PortService{
		db:          db,
		enabled:     enabled,
		ttl:         ttl,
		source:      gopsutilConnectionSource{},
		processName: lookupProcessName,
	}
}

func (s *PortService) Snapshot(ctx context.Context) PortSnapshot {
	return s.cache.get(ctx, s.ttl, s.collect)
}

func (s *PortService) Invalidate() {
	s.cache.invalidate()
}

func (s *PortService) collect(ctx context.Context) PortSnapshot {
	snapshot := PortSnapshot{
		Enabled:     s.enabled,
		Status:      "disabled",
		Listeners:   []PortListener{},
		CollectedAt: time.Now().UTC(),
	}
	if !s.enabled {
		return snapshot
	}

	projects, projectErr := s.db.ListProjects(ctx)
	projectPorts := make(map[uint32][]PortProject)
	for _, project := range projects {
		if project.Port > 0 {
			projectPorts[uint32(project.Port)] = append(projectPorts[uint32(project.Port)], PortProject{
				ID: project.ID, Name: project.Name,
			})
		}
	}

	var warnings []string
	var listeners []PortListener
	seen := make(map[string]bool)
	for _, protocol := range []string{"tcp", "udp"} {
		connections, err := s.source.Connections(ctx, protocol)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s socket details unavailable", strings.ToUpper(protocol)))
			continue
		}
		for _, connection := range connections {
			if connection.Laddr.Port == 0 {
				continue
			}
			if protocol == "tcp" && !strings.EqualFold(connection.Status, "LISTEN") {
				continue
			}
			address := normalizeBindAddress(connection.Laddr.IP)
			key := fmt.Sprintf("%s|%s|%d|%d", protocol, address, connection.Laddr.Port, connection.Pid)
			if seen[key] {
				continue
			}
			seen[key] = true
			exposure, exposureKey := classifyExposure(address)
			listener := PortListener{
				Protocol:    strings.ToUpper(protocol),
				Address:     address,
				Port:        connection.Laddr.Port,
				Exposure:    exposure,
				ExposureKey: exposureKey,
				PID:         connection.Pid,
				Projects:    projectPorts[connection.Laddr.Port],
			}
			if connection.Pid > 0 {
				listener.Process = s.processName(ctx, connection.Pid)
			}
			listeners = append(listeners, listener)
		}
	}

	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].Port != listeners[j].Port {
			return listeners[i].Port < listeners[j].Port
		}
		if listeners[i].Protocol != listeners[j].Protocol {
			return listeners[i].Protocol < listeners[j].Protocol
		}
		return listeners[i].Address < listeners[j].Address
	})

	listeningPorts := make(map[uint32]bool)
	for _, listener := range listeners {
		listeningPorts[listener.Port] = true
		if listener.Protocol == "TCP" {
			snapshot.TCPCount++
		} else {
			snapshot.UDPCount++
		}
	}
	for _, project := range projects {
		if project.Port > 0 && !listeningPorts[uint32(project.Port)] {
			snapshot.MissingProjectPorts = append(snapshot.MissingProjectPorts, MissingProjectPort{
				ID: project.ID, Name: project.Name, Port: project.Port,
			})
		}
	}
	sort.Slice(snapshot.MissingProjectPorts, func(i, j int) bool {
		return snapshot.MissingProjectPorts[i].Port < snapshot.MissingProjectPorts[j].Port
	})

	if projectErr != nil {
		warnings = append(warnings, "project port matching unavailable")
	}
	snapshot.Listeners = listeners
	snapshot.TotalCount = len(listeners)
	snapshot.Status = "online"
	if len(warnings) > 0 {
		snapshot.Status = "degraded"
		snapshot.Warning = strings.Join(warnings, "; ")
	}
	return snapshot
}

func lookupProcessName(ctx context.Context, pid int32) string {
	item, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return ""
	}
	name, err := item.NameWithContext(ctx)
	if err != nil {
		return ""
	}
	return name
}

func normalizeBindAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return "0.0.0.0"
	}
	return address
}

func classifyExposure(address string) (string, string) {
	if address == "0.0.0.0" || address == "::" || address == "*" {
		return "All interfaces", "all"
	}
	ip := net.ParseIP(strings.Trim(address, "[]"))
	if ip != nil && ip.IsLoopback() {
		return "Loopback only", "loopback"
	}
	return "Specific interface", "specific"
}
