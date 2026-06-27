package discovery

import (
	"strconv"
	"strings"
)

// SSHExecuter is the interface required by the collector.
type SSHExecuter interface {
	Exec(cmd string) (string, string, int, error)
}

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

func (c *Collector) runCommand(name, cmd string) StepResult {
	stdout, _, _, err := c.client.Exec(cmd)
	if err != nil {
		return StepResult{Name: name, Error: err}
	}
	return StepResult{Name: name, Value: strings.TrimSpace(stdout)}
}

func (c *Collector) runIntCommand(name, cmd string) StepResult {
	stdout, _, _, err := c.client.Exec(cmd)
	if err != nil {
		return StepResult{Name: name, Error: err}
	}

	value, parseErr := strconv.Atoi(strings.TrimSpace(stdout))
	if parseErr != nil {
		return StepResult{Name: name, Error: parseErr}
	}

	return StepResult{Name: name, IntValue: value}
}

func (c *Collector) runFloatCommand(name, cmd string) StepResult {
	stdout, _, _, err := c.client.Exec(cmd)
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
	return c.runCommand("hostname", "hostname")
}

// CollectOS collects the operating system pretty name.
func (c *Collector) CollectOS() StepResult {
	stdout, _, _, err := c.client.Exec(`cat /etc/os-release | grep PRETTY_NAME`)
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
	return c.runCommand("kernel", "uname -r")
}

// CollectArchitecture collects the CPU architecture.
func (c *Collector) CollectArchitecture() StepResult {
	return c.runCommand("architecture", "uname -m")
}

// CollectCPUModel collects the CPU model name.
func (c *Collector) CollectCPUModel() StepResult {
	stdout, _, _, err := c.client.Exec(`lscpu | grep "Model name"`)
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
	return c.runIntCommand("cpu_cores", "nproc")
}

// CollectRAM collects total RAM in megabytes.
func (c *Collector) CollectRAM() StepResult {
	stdout, _, _, err := c.client.Exec("free -m")
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
	return c.runFloatCommand("disk_total_gb", `df -BG / | awk 'NR==2{print $2}'`)
}

// CollectVirtualization collects the virtualization type.
func (c *Collector) CollectVirtualization() StepResult {
	stdout, _, _, err := c.client.Exec("systemd-detect-virt 2>/dev/null || echo none")
	if err != nil {
		return StepResult{Name: "virtualization", Error: err}
	}

	return StepResult{Name: "virtualization", Value: strings.TrimSpace(stdout)}
}

// CollectPublicIP collects the public IP address.
func (c *Collector) CollectPublicIP() StepResult {
	stdout, _, _, err := c.client.Exec("curl -s --max-time 5 ifconfig.me")
	if err != nil {
		return StepResult{Name: "public_ip", Error: err}
	}

	return StepResult{Name: "public_ip", Value: strings.TrimSpace(stdout)}
}

// CollectPrivateIP collects the first private IP address.
func (c *Collector) CollectPrivateIP() StepResult {
	stdout, _, _, err := c.client.Exec(`hostname -I | awk '{print $1}'`)
	if err != nil {
		return StepResult{Name: "private_ip", Error: err}
	}

	return StepResult{Name: "private_ip", Value: strings.TrimSpace(stdout)}
}

// CollectTimezone collects the current timezone.
func (c *Collector) CollectTimezone() StepResult {
	stdout, _, _, err := c.client.Exec(`timedatectl | grep "Time zone"`)
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
	stdout, _, _, err := c.client.Exec("curl -s --max-time 2 http://169.254.169.254/latest/meta-data/instance-id || echo unknown")
	if err != nil {
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
	return []StepResult{
		c.CollectHostname(),
		c.CollectOS(),
		c.CollectKernel(),
		c.CollectArchitecture(),
		c.CollectCPUModel(),
		c.CollectCPUCores(),
		c.CollectRAM(),
		c.CollectDisk(),
		c.CollectVirtualization(),
		c.CollectPublicIP(),
		c.CollectPrivateIP(),
		c.CollectTimezone(),
		c.CollectProvider(),
	}
}
