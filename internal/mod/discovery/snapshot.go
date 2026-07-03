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
	// Swap holds swap space information.
	Swap *SwapInfo `json:"swap,omitempty"`
	// Filesystems holds mounted filesystems.
	Filesystems []FilesystemInfo `json:"filesystems,omitempty"`
	// BlockDevices holds detected block devices.
	BlockDevices []BlockDeviceInfo `json:"blockDevices,omitempty"`
	// Locale holds the default locale string.
	Locale string `json:"locale,omitempty"`
	// Users holds local user accounts.
	Users []UserInfo `json:"users,omitempty"`
	// Groups holds local groups.
	Groups []GroupInfo `json:"groups,omitempty"`
	// SSHConfig holds sshd configuration details.
	SSHConfig *SSHConfigInfo `json:"sshConfig,omitempty"`
	// Firewall holds firewall configuration.
	Firewall *FirewallInfo `json:"firewall,omitempty"`
	// SELinux holds SELinux status.
	SELinux *SELinuxInfo `json:"selinux,omitempty"`
	// AppArmor holds AppArmor status.
	AppArmor *AppArmorInfo `json:"apparmor,omitempty"`
	// CronJobs holds detected cron jobs.
	CronJobs []CronJobInfo `json:"cronJobs,omitempty"`
	// Timers holds detected systemd timers.
	Timers []TimerInfo `json:"timers,omitempty"`
	// Runtimes holds detected language runtimes.
	Runtimes []RuntimeInfo `json:"runtimes,omitempty"`
	// ReverseProxies holds detected reverse proxy services.
	ReverseProxies []ReverseProxyInfo `json:"reverseProxies,omitempty"`
	// MessageQueues holds detected message queue services.
	MessageQueues []MessageQueueInfo `json:"messageQueues,omitempty"`
	// Monitoring holds detected monitoring services.
	Monitoring []MonitoringServiceInfo `json:"monitoring,omitempty"`
	// CI holds CI/CD detection information.
	CI *CIInfo `json:"ci,omitempty"`
	// SSL holds SSL certificate information.
	SSL []SSLCertInfo `json:"ssl,omitempty"`
	// DNS holds DNS resolver configuration.
	DNS *DNSInfo `json:"dns,omitempty"`
	// GitRepos holds detected git repositories.
	GitRepos []GitRepoInfo `json:"gitRepos,omitempty"`
	// ProcessManagers holds detected process managers.
	ProcessManagers []ProcessManagerInfo `json:"processManagers,omitempty"`
	// Containerd holds containerd information.
	Containerd *ContainerdInfo `json:"containerd,omitempty"`
	// Podman holds Podman information.
	Podman *PodmanInfo `json:"podman,omitempty"`
	// DockerRoot holds the Docker root directory.
	DockerRoot string `json:"dockerRoot,omitempty"`
	// StorageDriver holds the Docker storage driver.
	StorageDriver string `json:"storageDriver,omitempty"`
	// Services holds systemd services that are active.
	Services []SystemService `json:"services,omitempty"`
	// Packages holds installed package names.
	Packages []string `json:"packages,omitempty"`
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
	// RestartPolicy is the container restart policy.
	RestartPolicy string `json:"restartPolicy,omitempty"`
	// Healthcheck holds a summary of the healthcheck configuration.
	Healthcheck string `json:"healthcheck,omitempty"`
	// Command is the configured container command.
	Command string `json:"command,omitempty"`
	// Entrypoint is the configured container entrypoint.
	Entrypoint string `json:"entrypoint,omitempty"`
	// EnvVarNames lists environment variable names only.
	EnvVarNames []string `json:"envVarNames,omitempty"`
	// ComposeService is the compose service label.
	ComposeService string `json:"composeService,omitempty"`
	// ImageDigest is the image digest.
	ImageDigest string `json:"imageDigest,omitempty"`
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
	// Contents holds the raw compose file contents.
	Contents string `json:"contents,omitempty"`
	// Services lists the service names in the project.
	Services []string `json:"services,omitempty"`
	// Networks lists project networks.
	Networks []string `json:"networks,omitempty"`
	// Volumes lists project volumes.
	Volumes []string `json:"volumes,omitempty"`
	// DependsOn maps service names to their dependencies.
	DependsOn map[string][]string `json:"dependsOn,omitempty"`
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
	// Host is the database host.
	Host string `json:"host,omitempty"`
	// DataDir is the data directory path.
	DataDir string `json:"dataDir,omitempty"`
	// SizeMB is the total database size in MB (if accessible).
	SizeMB int64 `json:"sizeMb,omitempty"`
	// Running indicates whether the database process is running.
	Running bool `json:"running"`
	// Replication indicates whether replication is enabled.
	Replication bool `json:"replication,omitempty"`
	// ReplicaRole is the database replication role.
	ReplicaRole string `json:"replicaRole,omitempty"`
	// RedisUsage describes how Redis is used.
	RedisUsage string `json:"redisUsage,omitempty"`
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

// SwapInfo holds swap space information.
type SwapInfo struct {
	TotalMB int `json:"totalMb"`
	UsedMB  int `json:"usedMb"`
	FreeMB  int `json:"freeMb"`
}

// FilesystemInfo holds filesystem type and mount information.
type FilesystemInfo struct {
	Device       string  `json:"device"`
	MountPoint   string  `json:"mountPoint"`
	FSType       string  `json:"fsType"`
	TotalGB      float64 `json:"totalGb"`
	UsedGB       float64 `json:"usedGb"`
	AvailGB      float64 `json:"availGb"`
	UsePercent   float64 `json:"usePercent"`
	InodesTotal  int64   `json:"inodesTotal,omitempty"`
	InodesUsed   int64   `json:"inodesUsed,omitempty"`
	MountOptions string  `json:"mountOptions,omitempty"`
}

// BlockDeviceInfo holds block device information.
type BlockDeviceInfo struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	SizeGB     float64 `json:"sizeGb"`
	MountPoint string  `json:"mountPoint,omitempty"`
	FSType     string  `json:"fsType,omitempty"`
	Model      string  `json:"model,omitempty"`
	ReadOnly   bool    `json:"readOnly"`
}

// UserInfo holds local user account information.
type UserInfo struct {
	Username string   `json:"username"`
	UID      int      `json:"uid"`
	GID      int      `json:"gid"`
	HomeDir  string   `json:"homeDir,omitempty"`
	Shell    string   `json:"shell,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// GroupInfo holds local group information.
type GroupInfo struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`
}

// SSHConfigInfo holds SSH server configuration.
type SSHConfigInfo struct {
	Port           int      `json:"port"`
	PermitRootLogin string   `json:"permitRootLogin,omitempty"`
	PasswordAuth   string    `json:"passwordAuth,omitempty"`
	PubkeyAuth     string    `json:"pubkeyAuth,omitempty"`
	AuthorizedKeys []string  `json:"authorizedKeys,omitempty"`
	HostKeys       []string  `json:"hostKeys,omitempty"`
}

// FirewallInfo holds firewall configuration.
type FirewallInfo struct {
	Type       string   `json:"type"`
	Active     bool     `json:"active"`
	DefaultIn  string   `json:"defaultIn,omitempty"`
	DefaultOut string   `json:"defaultOut,omitempty"`
	Rules      []string `json:"rules,omitempty"`
}

// SELinuxInfo holds SELinux status.
type SELinuxInfo struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
	Policy  string `json:"policy,omitempty"`
}

// AppArmorInfo holds AppArmor status.
type AppArmorInfo struct {
	Enabled  bool     `json:"enabled"`
	Profiles []string `json:"profiles,omitempty"`
}

// CronJobInfo holds cron job information.
type CronJobInfo struct {
	User     string `json:"user"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Source   string `json:"source"`
}

// TimerInfo holds systemd timer information.
type TimerInfo struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule,omitempty"`
	Interval string `json:"interval,omitempty"`
	Active   bool   `json:"active"`
	LastRun  string `json:"lastRun,omitempty"`
	NextRun  string `json:"nextRun,omitempty"`
	Triggers string `json:"triggers,omitempty"`
}

// RuntimeInfo holds a detected runtime version.
type RuntimeInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`
}

// ReverseProxyInfo holds reverse proxy detection.
type ReverseProxyInfo struct {
	Type       string `json:"type"`
	Version    string `json:"version,omitempty"`
	ConfigPath string `json:"configPath,omitempty"`
	Active     bool   `json:"active"`
}

// MessageQueueInfo holds message queue detection.
type MessageQueueInfo struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Port    int    `json:"port,omitempty"`
	Running bool   `json:"running"`
}

// MonitoringServiceInfo holds monitoring service detection.
type MonitoringServiceInfo struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Port    int    `json:"port,omitempty"`
	Running bool   `json:"running"`
}

// CIInfo holds CI/CD detection information.
type CIInfo struct {
	Type        string   `json:"type"`
	Workflows   []string `json:"workflows,omitempty"`
	SelfHosted  bool     `json:"selfHosted"`
	SecretNames []string `json:"secretNames,omitempty"`
	DockerBuild bool     `json:"dockerBuild"`
	DockerPush  bool     `json:"dockerPush"`
	Registry    string   `json:"registry,omitempty"`
}

// SSLCertInfo holds SSL certificate information (general).
type SSLCertInfo struct {
	Domain        string `json:"domain"`
	Path          string `json:"path"`
	Expiry        string `json:"expiry,omitempty"`
	DaysRemaining int    `json:"daysRemaining,omitempty"`
	Issuer        string `json:"issuer,omitempty"`
	AutoRenew     bool   `json:"autoRenew,omitempty"`
}

// DNSInfo holds DNS configuration.
type DNSInfo struct {
	Nameservers  []string `json:"nameservers"`
	SearchDomain string   `json:"searchDomain,omitempty"`
}

// GitRepoInfo holds git repository information.
type GitRepoInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Remote string `json:"remote,omitempty"`
	IsDirty bool   `json:"isDirty"`
}

// ProcessManagerInfo holds process manager detection.
type ProcessManagerInfo struct {
	Type    string   `json:"type"`
	Version string   `json:"version,omitempty"`
	Apps    []string `json:"apps,omitempty"`
	Running bool     `json:"running"`
}

// ContainerdInfo holds containerd information.
type ContainerdInfo struct {
	Version string `json:"version,omitempty"`
	Running bool   `json:"running"`
}

// PodmanInfo holds Podman information.
type PodmanInfo struct {
	Version    string `json:"version,omitempty"`
	Containers int    `json:"containers,omitempty"`
}
