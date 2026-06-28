package discovery

import (
	"context"
	"strconv"
	"strings"

	"meshium/internal/mod/transport"
)

// SSHExecuter is the interface required by the collector.
// It is an alias for transport.SSHExecuter, which includes Upload/Download
// methods that discovery doesn't use but migration does. The real *ssh.Client
// satisfies the full interface.
type SSHExecuter = transport.SSHExecuter

// Command defines a single discovery command.
type Command struct {
	Name     string
	Cmd      string
	Parse    func(stdout string) string
	ParseInt func(stdout string) int
}

// Collector runs SSH commands to collect system info.
type Collector struct {
	client SSHExecuter
}

// NewCollector constructs a Collector for the provided SSH client.
func NewCollector(client SSHExecuter) *Collector {
	return &Collector{client: client}
}

func (c *Collector) runCommand(ctx context.Context, name, cmd string) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, cmd)
	if err != nil {
		return StepResult{Name: name, Error: err}
	}
	return StepResult{Name: name, Value: strings.TrimSpace(stdout)}
}

func (c *Collector) runIntCommand(ctx context.Context, name, cmd string) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, cmd)
	if err != nil {
		return StepResult{Name: name, Error: err}
	}

	value, parseErr := strconv.Atoi(strings.TrimSpace(stdout))
	if parseErr != nil {
		return StepResult{Name: name, Error: parseErr}
	}

	return StepResult{Name: name, IntValue: value}
}

func (c *Collector) runFloatCommand(ctx context.Context, name, cmd string) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, cmd)
	if err != nil {
		return StepResult{Name: name, Error: err}
	}

	value := strings.TrimSpace(stdout)
	value = strings.TrimSuffix(value, "G")
	parsed, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil {
		return StepResult{Name: name, Error: parseErr}
	}

	return StepResult{Name: name, FloatValue: parsed}
}

// CollectHostname collects the host name.
func (c *Collector) CollectHostname() StepResult {
	return c.CollectHostnameContext(context.Background())
}

// CollectHostnameContext is the context-aware variant of CollectHostname.
func (c *Collector) CollectHostnameContext(ctx context.Context) StepResult {
	return c.runCommand(ctx, "hostname", "hostname")
}

// CollectOS collects the operating system pretty name.
func (c *Collector) CollectOS() StepResult {
	return c.CollectOSContext(context.Background())
}

// CollectOSContext is the context-aware variant of CollectOS.
func (c *Collector) CollectOSContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, `cat /etc/os-release | grep PRETTY_NAME`)
	if err != nil {
		return StepResult{Name: "os", Error: err}
	}

	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return StepResult{Name: "os", Value: line}
	}

	return StepResult{Name: "os", Value: strings.Trim(parts[1], `"`)}
}

// CollectKernel collects the kernel release.
func (c *Collector) CollectKernel() StepResult {
	return c.CollectKernelContext(context.Background())
}

// CollectKernelContext is the context-aware variant of CollectKernel.
func (c *Collector) CollectKernelContext(ctx context.Context) StepResult {
	return c.runCommand(ctx, "kernel", "uname -r")
}

// CollectArchitecture collects the CPU architecture.
func (c *Collector) CollectArchitecture() StepResult {
	return c.CollectArchitectureContext(context.Background())
}

// CollectArchitectureContext is the context-aware variant of CollectArchitecture.
func (c *Collector) CollectArchitectureContext(ctx context.Context) StepResult {
	return c.runCommand(ctx, "architecture", "uname -m")
}

// CollectCPUModel collects the CPU model name.
func (c *Collector) CollectCPUModel() StepResult {
	return c.CollectCPUModelContext(context.Background())
}

// CollectCPUModelContext is the context-aware variant of CollectCPUModel.
func (c *Collector) CollectCPUModelContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, `lscpu | grep "Model name"`)
	if err != nil {
		return StepResult{Name: "cpu_model", Error: err}
	}

	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return StepResult{Name: "cpu_model", Value: line}
	}

	return StepResult{Name: "cpu_model", Value: strings.TrimSpace(parts[1])}
}

// CollectCPUCores collects the number of CPU cores.
func (c *Collector) CollectCPUCores() StepResult {
	return c.CollectCPUCoresContext(context.Background())
}

// CollectCPUCoresContext is the context-aware variant of CollectCPUCores.
func (c *Collector) CollectCPUCoresContext(ctx context.Context) StepResult {
	return c.runIntCommand(ctx, "cpu_cores", "nproc")
}

// CollectRAM collects total RAM in megabytes.
func (c *Collector) CollectRAM() StepResult {
	return c.CollectRAMContext(context.Background())
}

// CollectRAMContext is the context-aware variant of CollectRAM.
func (c *Collector) CollectRAMContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, "free -m")
	if err != nil {
		return StepResult{Name: "ram_total_mb", Error: err}
	}

	line := strings.TrimSpace(stdout)
	if strings.Contains(line, "\n") {
		for _, candidate := range strings.Split(line, "\n") {
			candidate = strings.TrimSpace(candidate)
			if strings.HasPrefix(candidate, "Mem:") {
				fields := strings.Fields(candidate)
				if len(fields) >= 2 {
					value, parseErr := strconv.Atoi(fields[1])
					if parseErr != nil {
						return StepResult{Name: "ram_total_mb", Error: parseErr}
					}
					return StepResult{Name: "ram_total_mb", IntValue: value}
				}
			}
		}
	}

	if value, parseErr := strconv.Atoi(line); parseErr == nil {
		return StepResult{Name: "ram_total_mb", IntValue: value}
	}

	fields := strings.Fields(line)
	if len(fields) >= 2 && fields[0] == "Mem:" {
		value, parseErr := strconv.Atoi(fields[1])
		if parseErr != nil {
			return StepResult{Name: "ram_total_mb", Error: parseErr}
		}
		return StepResult{Name: "ram_total_mb", IntValue: value}
	}

	return StepResult{Name: "ram_total_mb", Error: strconv.ErrSyntax}
}

// CollectDisk collects the total root disk size in gigabytes.
func (c *Collector) CollectDisk() StepResult {
	return c.CollectDiskContext(context.Background())
}

// CollectDiskContext is the context-aware variant of CollectDisk.
func (c *Collector) CollectDiskContext(ctx context.Context) StepResult {
	return c.runFloatCommand(ctx, "disk_total_gb", `df -BG / | awk 'NR==2{print $2}'`)
}

// CollectVirtualization collects the virtualization type.
func (c *Collector) CollectVirtualization() StepResult {
	return c.CollectVirtualizationContext(context.Background())
}

// CollectVirtualizationContext is the context-aware variant of CollectVirtualization.
func (c *Collector) CollectVirtualizationContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, "systemd-detect-virt 2>/dev/null || echo none")
	if err != nil {
		return StepResult{Name: "virtualization", Error: err}
	}

	return StepResult{Name: "virtualization", Value: strings.TrimSpace(stdout)}
}

// CollectPublicIP collects the public IP address.
func (c *Collector) CollectPublicIP() StepResult {
	return c.CollectPublicIPContext(context.Background())
}

// CollectPublicIPContext is the context-aware variant of CollectPublicIP.
func (c *Collector) CollectPublicIPContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, "curl -s --max-time 5 ifconfig.me")
	if err != nil {
		return StepResult{Name: "public_ip", Error: err}
	}

	return StepResult{Name: "public_ip", Value: strings.TrimSpace(stdout)}
}

// CollectPrivateIP collects the first private IP address.
func (c *Collector) CollectPrivateIP() StepResult {
	return c.CollectPrivateIPContext(context.Background())
}

// CollectPrivateIPContext is the context-aware variant of CollectPrivateIP.
func (c *Collector) CollectPrivateIPContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, `hostname -I | awk '{print $1}'`)
	if err != nil {
		return StepResult{Name: "private_ip", Error: err}
	}

	return StepResult{Name: "private_ip", Value: strings.TrimSpace(stdout)}
}

// CollectTimezone collects the current timezone.
func (c *Collector) CollectTimezone() StepResult {
	return c.CollectTimezoneContext(context.Background())
}

// CollectTimezoneContext is the context-aware variant of CollectTimezone.
func (c *Collector) CollectTimezoneContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, `timedatectl | grep "Time zone"`)
	if err != nil {
		return StepResult{Name: "timezone", Error: err}
	}

	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return StepResult{Name: "timezone", Value: line}
	}

	return StepResult{Name: "timezone", Value: strings.TrimSpace(parts[1])}
}

// CollectProvider identifies the cloud provider when metadata is available.
func (c *Collector) CollectProvider() StepResult {
	return c.CollectProviderContext(context.Background())
}

// CollectProviderContext is the context-aware variant of CollectProvider.
func (c *Collector) CollectProviderContext(ctx context.Context) StepResult {
	stdout, _, _, err := c.client.ExecContext(ctx, "curl -s --max-time 2 http://169.254.169.254/latest/meta-data/instance-id || echo unknown")
	if err != nil {
		if c.client != nil && !c.client.IsAlive() {
			return StepResult{Name: "provider", Error: err}
		}
		return StepResult{Name: "provider", Value: "unknown"}
	}

	value := strings.TrimSpace(stdout)
	if value == "" || value == "unknown" {
		return StepResult{Name: "provider", Value: "unknown"}
	}

	return StepResult{Name: "provider", Value: "cloud"}
}

// CollectAll runs all collection steps and returns results.
func (c *Collector) CollectAll() []StepResult {
	return c.CollectAllContext(context.Background())
}

// CollectAllContext runs all collection steps with the provided context
// for cancellation and returns results.
func (c *Collector) CollectAllContext(ctx context.Context) []StepResult {
	return []StepResult{
		c.CollectHostnameContext(ctx),
		c.CollectOSContext(ctx),
		c.CollectKernelContext(ctx),
		c.CollectArchitectureContext(ctx),
		c.CollectCPUModelContext(ctx),
		c.CollectCPUCoresContext(ctx),
		c.CollectRAMContext(ctx),
		c.CollectDiskContext(ctx),
		c.CollectVirtualizationContext(ctx),
		c.CollectPublicIPContext(ctx),
		c.CollectPrivateIPContext(ctx),
		c.CollectTimezoneContext(ctx),
		c.CollectProviderContext(ctx),
	}
}
