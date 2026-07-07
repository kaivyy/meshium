package discovery

import (
	"context"
	"strconv"
	"strings"
	"sync"

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

// CollectAllLocalContext runs all local discovery commands in a SINGLE SSH
// round-trip, then parses the output into individual StepResults.
// This replaces 11 sequential SSH commands with 1, cutting latency from
// ~2.5s to ~0.1s for the local portion of the connection test.
func (c *Collector) CollectAllLocalContext(ctx context.Context) []StepResult {
	// All local commands combined into one shell script.
	// Each line outputs a value separated by a delimiter.
	const cmd = `echo "===HOSTNAME==="; hostname;
echo "===OS==="; cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d= -f2 | tr -d '"';
echo "===KERNEL==="; uname -r;
echo "===ARCH==="; uname -m;
echo "===CPU_MODEL==="; grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs;
echo "===CPU_CORES==="; nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo;
echo "===RAM==="; free -m 2>/dev/null | awk '/^Mem:/ {print $2}';
echo "===DISK==="; df -l --output=size / 2>/dev/null | tail -1 | awk '{printf "%.1f\n", $1/1024/1024}';
echo "===VIRT==="; systemd-detect-virt 2>/dev/null || echo unknown;
echo "===PRIVATE_IP==="; hostname -I 2>/dev/null | awk '{print $1}';
echo "===TIMEZONE==="; timedatectl 2>/dev/null | grep "Time zone" | cut -d: -f2 | xargs || cat /etc/timezone 2>/dev/null;
echo "===END===";`

	stdout, _, _, err := c.client.ExecContext(ctx, cmd)
	if err != nil {
		// Fallback: run individual commands
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
			c.CollectPrivateIPContext(ctx),
			c.CollectTimezoneContext(ctx),
		}
	}

	results := make([]StepResult, 0, 11)
	lines := strings.Split(stdout, "\n")

	type section struct {
		name   string
		values []string
	}
	var sections []section
	currentName := ""
	currentValues := []string{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "===") && strings.HasSuffix(line, "===") {
			if currentName != "" {
				sections = append(sections, section{name: currentName, values: currentValues})
			}
			currentName = strings.TrimPrefix(strings.TrimSuffix(line, "==="), "===")
			currentValues = []string{}
		} else if line != "" {
			currentValues = append(currentValues, line)
		}
	}

	for _, sec := range sections {
		value := strings.Join(sec.values, " ")
		value = strings.TrimSpace(value)

		switch sec.name {
		case "HOSTNAME":
			results = append(results, StepResult{Name: "hostname", Value: value})
		case "OS":
			results = append(results, StepResult{Name: "os", Value: value})
		case "KERNEL":
			results = append(results, StepResult{Name: "kernel", Value: value})
		case "ARCH":
			results = append(results, StepResult{Name: "architecture", Value: value})
		case "CPU_MODEL":
			results = append(results, StepResult{Name: "cpu_model", Value: value})
		case "CPU_CORES":
			n, _ := strconv.Atoi(value)
			results = append(results, StepResult{Name: "cpu_cores", IntValue: n})
		case "RAM":
			n, _ := strconv.Atoi(value)
			results = append(results, StepResult{Name: "ram_total_mb", IntValue: n})
		case "DISK":
			f, _ := strconv.ParseFloat(value, 64)
			results = append(results, StepResult{Name: "disk_total_gb", FloatValue: f})
		case "VIRT":
			results = append(results, StepResult{Name: "virtualization", Value: value})
		case "PRIVATE_IP":
			results = append(results, StepResult{Name: "private_ip", Value: value})
		case "TIMEZONE":
			results = append(results, StepResult{Name: "timezone", Value: value})
		}
	}

	return results
}

// CollectNetworkInfoContext runs public_ip and provider detection in parallel
// and calls onStep for each result as soon as it completes.
// This way the frontend sees each result immediately rather than waiting
// for both to finish.
func (c *Collector) CollectNetworkInfoContext(ctx context.Context, onStep func(StepResult)) {
	var wg sync.WaitGroup
	wg.Add(2)

	// The two goroutines below call onStep concurrently, so serialize the
	// callback to protect whatever shared state the caller mutates inside it
	// (e.g. RunConnectionTest writes to a shared SystemInfo). Callers must not
	// assume any ordering between public_ip and provider results.
	var stepMu sync.Mutex
	safeStep := func(sr StepResult) {
		stepMu.Lock()
		defer stepMu.Unlock()
		onStep(sr)
	}

	// public_ip: curl ifconfig.me (can take 1-5s)
	go func() {
		defer wg.Done()
		stdout, _, _, err := c.client.ExecContext(ctx, "curl -s --max-time 3 ifconfig.me")
		if err != nil {
			safeStep(StepResult{Name: "public_ip", Error: err})
			return
		}
		safeStep(StepResult{Name: "public_ip", Value: strings.TrimSpace(stdout)})
	}()

	// provider: curl 169.254.169.254 (AWS metadata, times out in 2s on non-AWS)
	go func() {
		defer wg.Done()
		stdout, _, _, err := c.client.ExecContext(ctx, "curl -s --max-time 1 http://169.254.169.254/latest/meta-data/instance-id 2>/dev/null || echo unknown")
		if err != nil {
			safeStep(StepResult{Name: "provider", Value: "unknown"})
			return
		}
		value := strings.TrimSpace(stdout)
		if value == "" || value == "unknown" {
			safeStep(StepResult{Name: "provider", Value: "unknown"})
			return
		}
		safeStep(StepResult{Name: "provider", Value: "cloud"})
	}()

	wg.Wait()
}

// CollectAll runs all collection steps and returns results.
func (c *Collector) CollectAll() []StepResult {
	return c.CollectAllContext(context.Background())
}

// CollectAllContext runs all collection steps with the provided context
// for cancellation and returns results.
func (c *Collector) CollectAllContext(ctx context.Context) []StepResult {
	var all []StepResult
	all = append(all, c.CollectAllLocalContext(ctx)...)
	// Collect network info synchronously, collecting results
	var mu sync.Mutex
	c.CollectNetworkInfoContext(ctx, func(sr StepResult) {
		mu.Lock()
		all = append(all, sr)
		mu.Unlock()
	})
	return all
}
