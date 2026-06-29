package discovery

import "time"

// ServerSnapshot is a comprehensive snapshot of a server's state at a point
// in time. It is the output of the discovery engine and the input to the
// migration planner (Phase 5).
type ServerSnapshot struct {
	// CapturedAt is when the snapshot was taken.
	CapturedAt time.Time `json:"capturedAt"`
	// OS holds operating system information.
	OS OSInfo `json:"os"`
	// Hardware holds hardware resource information.
	Hardware HardwareInfo `json:"hardware"`
	// Docker holds Docker container/image information.
	// nil if Docker is not installed.
	Docker *DockerInfo `json:"docker,omitempty"`
	// Services holds systemd services that are active.
	Services []SystemService `json:"services,omitempty"`
	// Databases holds detected database instances.
	Databases []DatabaseInfo `json:"databases,omitempty"`
	// Nginx holds Nginx configuration information.
	// nil if Nginx is not installed.
	Nginx *NginxInfo `json:"nginx,omitempty"`
	// DiskUsage holds per-partition disk usage.
	DiskUsage []DiskPartition `json:"diskUsage,omitempty"`
	// NetworkPorts holds open/listening TCP ports.
	NetworkPorts []OpenPort `json:"networkPorts,omitempty"`
	// CollectionErrors holds errors from individual collectors.
	// A snapshot may be partial if some collectors failed.
	CollectionErrors []CollectorError `json:"collectionErrors,omitempty"`
}

// CollectorError records a failure from a specific collector.
type CollectorError struct {
	Collector string `json:"collector"`
	Error     string `json:"error"`
}

// --- OSInfo ---

// OSInfo holds operating system information.
type OSInfo struct {
	// Distro is the OS pretty name (e.g., "Ubuntu 22.04.3 LTS").
	Distro string `json:"distro"`
	// Kernel is the kernel release (e.g., "5.15.0-91-generic").
	Kernel string `json:"kernel"`
	// Architecture is the CPU architecture (e.g., "x86_64").
	Architecture string `json:"architecture"`
	// Timezone is the system timezone.
	Timezone string `json:"timezone"`
	// Uptime is the system uptime in seconds.
	Uptime int64 `json:"uptimeSeconds"`
	// Hostname is the server hostname.
	Hostname string `json:"hostname"`
	// Virtualization is the virtualization type (e.g., "kvm", "none").
	Virtualization string `json:"virtualization"`
}

// --- HardwareInfo ---

// HardwareInfo holds hardware resource information.
type HardwareInfo struct {
	// CPUModel is the CPU model name.
	CPUModel string `json:"cpuModel"`
	// CPUCores is the number of CPU cores.
	CPUCores int `json:"cpuCores"`
	// RAMTotalMB is total RAM in megabytes.
	RAMTotalMB int `json:"ramTotalMb"`
	// RAMUsedMB is used RAM in megabytes.
	RAMUsedMB int `json:"ramUsedMb"`
	// DiskTotalGB is total disk space in gigabytes.
	DiskTotalGB float64 `json:"diskTotalGb"`
	// DiskUsedGB is used disk space in gigabytes.
	DiskUsedGB float64 `json:"diskUsedGb"`
}

// --- DockerInfo ---

// DockerInfo holds Docker container, image, and compose project information.
type DockerInfo struct {
	// Version is the Docker server version.
	Version string `json:"version"`
	// Containers holds all containers (running and stopped).
	Containers []ContainerInfo `json:"containers,omitempty"`
	// Images holds all images.
	Images []ImageInfo `json:"images,omitempty"`
	// ComposeProjects holds docker-compose projects.
	ComposeProjects []ComposeProject `json:"composeProjects,omitempty"`
}

// ContainerInfo holds information about a single Docker container.
type ContainerInfo struct {
	// Name is the container name.
	Name string `json:"name"`
	// Image is the image name:tag.
	Image string `json:"image"`
	// Status is the container status (e.g., "Up 3 days", "Exited").
	Status string `json:"status"`
	// State is the container state (running, exited, paused, etc.).
	State string `json:"state"`
	// Ports holds port mappings.
	Ports []PortMapping `json:"ports,omitempty"`
	// Volumes holds volume mounts.
	Volumes []string `json:"volumes,omitempty"`
	// Networks holds network names the container is connected to.
	Networks []string `json:"networks,omitempty"`
	// Labels holds container labels.
	Labels map[string]string `json:"labels,omitempty"`
}

// PortMapping represents a Docker port mapping.
type PortMapping struct {
	// HostPort is the port on the host.
	HostPort int `json:"hostPort"`
	// ContainerPort is the port inside the container.
	ContainerPort int `json:"containerPort"`
	// Protocol is the protocol (tcp or udp).
	Protocol string `json:"protocol"`
}

// ImageInfo holds information about a Docker image.
type ImageInfo struct {
	// Repository is the image repository.
	Repository string `json:"repository"`
	// Tag is the image tag.
	Tag string `json:"tag"`
	// ID is the image ID (short).
	ID string `json:"id"`
	// Size is the image size in human-readable format.
	Size string `json:"size"`
}

// ComposeProject holds information about a docker-compose project.
type ComposeProject struct {
	// Name is the project name.
	Name string `json:"name"`
	// ConfigFiles is the path to the compose file(s).
	ConfigFiles string `json:"configFiles"`
	// Services lists the service names in the project.
	Services []string `json:"services,omitempty"`
}

// --- SystemService ---

// SystemService holds information about a systemd service.
type SystemService struct {
	// Name is the service name (e.g., "nginx.service").
	Name string `json:"name"`
	// Description is the service description.
	Description string `json:"description"`
	// LoadState is the load state (e.g., "loaded").
	LoadState string `json:"loadState"`
	// ActiveState is the active state (e.g., "active", "inactive").
	ActiveState string `json:"activeState"`
	// SubState is the sub state (e.g., "running", "dead").
	SubState string `json:"subState"`
	// Type is the service type (e.g., "simple", "forking").
	Type string `json:"type"`
	// DependsOn lists services that this service depends on (After/Requires).
	DependsOn []string `json:"dependsOn,omitempty"`
}

// --- DatabaseInfo ---

// DatabaseInfo holds information about a detected database instance.
type DatabaseInfo struct {
	// Type is the database type (mysql, postgresql, mongodb, redis).
	Type string `json:"type"`
	// Version is the database version.
	Version string `json:"version"`
	// Port is the port the database is listening on.
	Port int `json:"port"`
	// ProcessName is the process name (e.g., "mysqld", "postgres").
	ProcessName string `json:"processName"`
	// DataDir is the data directory path.
	DataDir string `json:"dataDir,omitempty"`
	// SizeMB is the total database size in MB (if accessible).
	SizeMB int64 `json:"sizeMb,omitempty"`
	// Running indicates whether the database process is running.
	Running bool `json:"running"`
}

// --- NginxInfo ---

// NginxInfo holds Nginx configuration information.
type NginxInfo struct {
	// Version is the Nginx version.
	Version string `json:"version"`
	// VHosts holds virtual host configurations.
	VHosts []NginxVHost `json:"vhosts,omitempty"`
	// Upstreams holds upstream server configurations.
	Upstreams []UpstreamConfig `json:"upstreams,omitempty"`
	// SSLCerts holds SSL certificate information.
	SSLCerts []SSLCert `json:"sslCerts,omitempty"`
}

// NginxVHost holds information about a single Nginx virtual host.
type NginxVHost struct {
	// ServerName is the server_name directive.
	ServerName string `json:"serverName"`
	// Listen is the listen port(s).
	Listen string `json:"listen"`
	// Root is the document root.
	Root string `json:"root,omitempty"`
	// ProxyPass is the proxy_pass destination (if any).
	ProxyPass string `json:"proxyPass,omitempty"`
	// ConfigFile is the config file path.
	ConfigFile string `json:"configFile"`
}

// UpstreamConfig holds an Nginx upstream block.
type UpstreamConfig struct {
	// Name is the upstream name.
	Name string `json:"name"`
	// Servers lists the upstream servers.
	Servers []UpstreamServer `json:"servers,omitempty"`
}

// UpstreamServer holds a single server in an upstream block.
type UpstreamServer struct {
	// Address is the server address (host:port).
	Address string `json:"address"`
	// Weight is the server weight (if specified).
	Weight int `json:"weight,omitempty"`
}

// SSLCert holds SSL certificate information.
type SSLCert struct {
	// Domain is the domain name.
	Domain string `json:"domain"`
	// Path is the certificate file path.
	Path string `json:"path"`
	// Expiry is the certificate expiry date.
	Expiry time.Time `json:"expiry"`
	// DaysRemaining is the days until expiry.
	DaysRemaining int `json:"daysRemaining"`
	// Issuer is the certificate issuer.
	Issuer string `json:"issuer,omitempty"`
}

// --- DiskPartition ---

// DiskPartition holds disk usage for a single partition/mount point.
type DiskPartition struct {
	// Filesystem is the device name.
	Filesystem string `json:"filesystem"`
	// MountPoint is the mount point.
	MountPoint string `json:"mountPoint"`
	// SizeGB is the total size in GB.
	SizeGB float64 `json:"sizeGb"`
	// UsedGB is the used space in GB.
	UsedGB float64 `json:"usedGb"`
	// AvailGB is the available space in GB.
	AvailGB float64 `json:"availGb"`
	// UsePercent is the usage percentage.
	UsePercent float64 `json:"usePercent"`
}

// --- OpenPort ---

// OpenPort holds information about an open/listening TCP port.
type OpenPort struct {
	// Port is the port number.
	Port int `json:"port"`
	// Protocol is the protocol (tcp or udp).
	Protocol string `json:"protocol"`
	// Process is the process name using the port.
	Process string `json:"process,omitempty"`
	// PID is the process ID.
	PID int `json:"pid,omitempty"`
	// Address is the bind address (e.g., "0.0.0.0", "127.0.0.1").
	Address string `json:"address,omitempty"`
}
