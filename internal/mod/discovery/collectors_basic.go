package discovery

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// --- OSCollector ---

// OSCollector collects operating system information.
type OSCollector struct{}

func (c *OSCollector) Name() string { return "os" }
func (c *OSCollector) Timeout() time.Duration { return 15 * time.Second }

func (c *OSCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	info := &OSInfo{}

	// Hostname
	if out, _, _, err := exec.ExecContext(ctx, "hostname"); err == nil {
		info.Hostname = strings.TrimSpace(out)
	}

	// OS pretty name
	if out, _, _, err := exec.ExecContext(ctx, `cat /etc/os-release | grep PRETTY_NAME`); err == nil {
		line := strings.TrimSpace(out)
		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			info.Distro = strings.Trim(parts[1], `"`)
		}
	}

	// Kernel
	if out, _, _, err := exec.ExecContext(ctx, "uname -r"); err == nil {
		info.Kernel = strings.TrimSpace(out)
	}

	// Architecture
	if out, _, _, err := exec.ExecContext(ctx, "uname -m"); err == nil {
		info.Architecture = strings.TrimSpace(out)
	}

	// Timezone
	if out, _, _, err := exec.ExecContext(ctx, `timedatectl | grep "Time zone"`); err == nil {
		line := strings.TrimSpace(out)
		if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
			info.Timezone = strings.TrimSpace(parts[1])
		}
	}

	// Uptime in seconds
	if out, _, _, err := exec.ExecContext(ctx, `cat /proc/uptime | awk '{print int($1)}'`); err == nil {
		if uptime, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64); perr == nil {
			info.Uptime = uptime
		}
	}

	// Virtualization
	if out, _, _, err := exec.ExecContext(ctx, "systemd-detect-virt 2>/dev/null || echo none"); err == nil {
		info.Virtualization = strings.TrimSpace(out)
	}

	return info, nil
}

// --- HardwareCollector ---

// HardwareCollector collects hardware resource information.
type HardwareCollector struct{}

func (c *HardwareCollector) Name() string { return "hardware" }
func (c *HardwareCollector) Timeout() time.Duration { return 15 * time.Second }

func (c *HardwareCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	info := &HardwareInfo{}

	// CPU model
	if out, _, _, err := exec.ExecContext(ctx, `lscpu | grep "Model name"`); err == nil {
		line := strings.TrimSpace(out)
		if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
			info.CPUModel = strings.TrimSpace(parts[1])
		}
	}

	// CPU cores
	if out, _, _, err := exec.ExecContext(ctx, "nproc"); err == nil {
		if cores, perr := strconv.Atoi(strings.TrimSpace(out)); perr == nil {
			info.CPUCores = cores
		}
	}

	// RAM total and used
	if out, _, _, err := exec.ExecContext(ctx, "free -m"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Mem:") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					if total, perr := strconv.Atoi(fields[1]); perr == nil {
						info.RAMTotalMB = total
					}
					if used, perr := strconv.Atoi(fields[2]); perr == nil {
						info.RAMUsedMB = used
					}
				}
			}
		}
	}

	// Disk total and used (root partition)
	if out, _, _, err := exec.ExecContext(ctx, `df -BG / | awk 'NR==2{print $2, $3}'`); err == nil {
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) >= 2 {
			if total, perr := strconv.ParseFloat(strings.TrimSuffix(fields[0], "G"), 64); perr == nil {
				info.DiskTotalGB = total
			}
			if used, perr := strconv.ParseFloat(strings.TrimSuffix(fields[1], "G"), 64); perr == nil {
				info.DiskUsedGB = used
			}
		}
	}

	return info, nil
}

// --- DiskCollector ---

// DiskCollector collects per-partition disk usage.
type DiskCollector struct{}

func (c *DiskCollector) Name() string { return "disk" }
func (c *DiskCollector) Timeout() time.Duration { return 15 * time.Second }

func (c *DiskCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	// df -BG shows sizes in GB units
	cmd := `df -BG | awk 'NR>1 && $1 !~ /^tmpfs|^devtmpfs|^overlay/ {print $1, $6, $2, $3, $4, $5}'`
	out, _, _, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("df failed: %w", err)
	}

	var partitions []DiskPartition
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		sizeGB, _ := strconv.ParseFloat(strings.TrimSuffix(fields[2], "G"), 64)
		usedGB, _ := strconv.ParseFloat(strings.TrimSuffix(fields[3], "G"), 64)
		availGB, _ := strconv.ParseFloat(strings.TrimSuffix(fields[4], "G"), 64)
		usePct, _ := strconv.ParseFloat(strings.TrimSuffix(fields[5], "%"), 64)

		partitions = append(partitions, DiskPartition{
			Filesystem:  fields[0],
			MountPoint:  fields[1],
			SizeGB:      sizeGB,
			UsedGB:      usedGB,
			AvailGB:     availGB,
			UsePercent:  usePct,
		})
	}

	return partitions, nil
}

// --- PortCollector ---

// PortCollector collects open/listening TCP ports.
type PortCollector struct{}

func (c *PortCollector) Name() string { return "port" }
func (c *PortCollector) Timeout() time.Duration { return 15 * time.Second }

func (c *PortCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	// Try ss first, fall back to netstat
	cmd := `ss -tlnp 2>/dev/null | awk 'NR>1 {print $4, $6}' || netstat -tlnp 2>/dev/null | awk 'NR>2 {print $4, $7}'`
	out, _, _, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("ss/netstat failed: %w", err)
	}

	var ports []OpenPort
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}

		// Parse address:port from the local address field
		addrPort := fields[0]
		addr, portStr := parseAddrPort(addrPort)
		if portStr == "" {
			continue
		}
		port, perr := strconv.Atoi(portStr)
		if perr != nil {
			continue
		}

		// Parse process info from the last field
		process, pid := "", 0
		if len(fields) >= 2 {
			process, pid = parseProcessInfo(fields[len(fields)-1])
		}

		ports = append(ports, OpenPort{
			Port:    port,
			Protocol: "tcp",
			Process: process,
			PID:     pid,
			Address: addr,
		})
	}

	return ports, nil
}

// parseAddrPort parses an address:port string (e.g., "0.0.0.0:80" or "[::]:443").
func parseAddrPort(s string) (addr, port string) {
	// Handle IPv6: [::]:80
	if strings.HasPrefix(s, "[") {
		if idx := strings.LastIndex(s, "]"); idx >= 0 {
			addr = s[1:idx]
			rest := s[idx+1:]
			if strings.HasPrefix(rest, ":") {
				port = rest[1:]
			}
			return
		}
	}
	// Handle IPv4: 0.0.0.0:80 or *:80
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		addr = s[:idx]
		port = s[idx+1:]
	}
	return
}

// parseProcessInfo parses a process info string like "users:(("nginx",pid=1234,fd=6))".
func parseProcessInfo(s string) (process string, pid int) {
	// Extract process name
	if idx := strings.Index(s, `(("`); idx >= 0 {
		rest := s[idx+3:]
		if endIdx := strings.Index(rest, `"`); endIdx >= 0 {
			process = rest[:endIdx]
		}
	}
	// Extract PID
	if idx := strings.Index(s, "pid="); idx >= 0 {
		rest := s[idx+4:]
		endIdx := strings.IndexAny(rest, ",)")
		if endIdx < 0 {
			endIdx = len(rest)
		}
		pid, _ = strconv.Atoi(rest[:endIdx])
	}
	return
}
