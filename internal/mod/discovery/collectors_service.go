package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// --- DockerCollector ---

// DockerCollector collects Docker container, image, and compose project info.
type DockerCollector struct{}

func (c *DockerCollector) Name() string { return "docker" }
func (c *DockerCollector) Timeout() time.Duration { return 30 * time.Second }

func (c *DockerCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	// Check if Docker is installed
	if out, _, exitCode, _ := exec.ExecContext(ctx, "which docker 2>/dev/null"); exitCode != 0 || strings.TrimSpace(out) == "" {
		return nil, nil // Docker not installed — not an error
	}

	info := &DockerInfo{}

	// Docker version
	if out, _, _, err := exec.ExecContext(ctx, "docker version --format '{{.Server.Version}}' 2>/dev/null"); err == nil {
		info.Version = strings.TrimSpace(out)
	}

	// Containers (use JSON format for reliable parsing)
	if out, _, _, err := exec.ExecContext(ctx, `docker ps -a --format '{{json .}}' 2>/dev/null`); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			var c dockerContainerJSON
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				continue
			}
			info.Containers = append(info.Containers, c.toContainerInfo())
		}
	}

	// Images
	if out, _, _, err := exec.ExecContext(ctx, `docker images --format '{{json .}}' 2>/dev/null`); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			var img dockerImageJSON
			if err := json.Unmarshal([]byte(line), &img); err != nil {
				continue
			}
			info.Images = append(info.Images, ImageInfo{
				Repository: img.Repository,
				Tag:        img.Tag,
				ID:         img.ID,
				Size:       img.Size,
			})
		}
	}

	// Compose projects (detect via container labels)
	if len(info.Containers) > 0 {
		projects := make(map[string]*ComposeProject)
		for _, c := range info.Containers {
			if projName, ok := c.Labels["com.docker.compose.project"]; ok {
				proj, exists := projects[projName]
				if !exists {
					proj = &ComposeProject{Name: projName}
					projects[projName] = proj
				}
				if svc, ok := c.Labels["com.docker.compose.service"]; ok {
					proj.Services = append(proj.Services, svc)
				}
				if cfg, ok := c.Labels["com.docker.compose.project.config_files"]; ok && proj.ConfigFiles == "" {
					proj.ConfigFiles = cfg
				}
			}
		}
		for _, proj := range projects {
			info.ComposeProjects = append(info.ComposeProjects, *proj)
		}
	}

	return info, nil
}

// dockerContainerJSON is the JSON output of `docker ps --format '{{json .}}'`.
type dockerContainerJSON struct {
	Name      string            `json:"Names"`
	Image     string            `json:"Image"`
	Status    string            `json:"Status"`
	State     string            `json:"State"`
	Ports     string            `json:"Ports"`
	Labels    string            `json:"Labels"`
	Networks  string            `json:"Networks"`
	Mounts    string            `json:"Mounts"`
}

func (c dockerContainerJSON) toContainerInfo() ContainerInfo {
	ci := ContainerInfo{
		Name:   strings.TrimSpace(c.Name),
		Image:  c.Image,
		Status: c.Status,
		State:  c.State,
	}

	// Parse ports (e.g., "0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp")
	for _, p := range strings.Split(c.Ports, ", ") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ci.Ports = append(ci.Ports, parseDockerPort(p))
	}

	// Parse labels
	if c.Labels != "" {
		ci.Labels = make(map[string]string)
		for _, pair := range strings.Split(c.Labels, ",") {
			pair = strings.TrimSpace(pair)
			if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
				ci.Labels[parts[0]] = parts[1]
			}
		}
	}

	// Parse networks
	if c.Networks != "" {
		for _, n := range strings.Split(c.Networks, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				ci.Networks = append(ci.Networks, n)
			}
		}
	}

	// Parse mounts
	if c.Mounts != "" {
		for _, m := range strings.Split(c.Mounts, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				ci.Volumes = append(ci.Volumes, m)
			}
		}
	}

	return ci
}

// parseDockerPort parses a Docker port string like "0.0.0.0:80->80/tcp".
func parseDockerPort(s string) PortMapping {
	pm := PortMapping{Protocol: "tcp"}
	// Handle "80/tcp" (no host mapping)
	if !strings.Contains(s, "->") {
		if idx := strings.Index(s, "/"); idx >= 0 {
			pm.ContainerPort, _ = strconv.Atoi(s[:idx])
			pm.Protocol = s[idx+1:]
		}
		return pm
	}
	// Handle "0.0.0.0:80->80/tcp"
	parts := strings.SplitN(s, "->", 2)
	if len(parts) == 2 {
		hostPart := parts[0]
		containerPart := parts[1]
		if idx := strings.LastIndex(hostPart, ":"); idx >= 0 {
			pm.HostPort, _ = strconv.Atoi(hostPart[idx+1:])
		}
		if idx := strings.Index(containerPart, "/"); idx >= 0 {
			pm.ContainerPort, _ = strconv.Atoi(containerPart[:idx])
			pm.Protocol = containerPart[idx+1:]
		}
	}
	return pm
}

// dockerImageJSON is the JSON output of `docker images --format '{{json .}}'`.
type dockerImageJSON struct {
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	ID         string `json:"ID"`
	Size       string `json:"Size"`
}

// --- ServiceCollector ---

// ServiceCollector collects systemd services that are active.
type ServiceCollector struct{}

func (c *ServiceCollector) Name() string { return "service" }
func (c *ServiceCollector) Timeout() time.Duration { return 30 * time.Second }

func (c *ServiceCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	// List active services with properties
	cmd := `systemctl list-units --type=service --state=active --no-legend --no-pager 2>/dev/null | awk '{print $1}'`
	out, _, _, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units failed: %w", err)
	}

	var services []SystemService
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}

		svc := SystemService{Name: name}

		// Get service properties
		propsCmd := fmt.Sprintf(`systemctl show '%s' --property=Description,LoadState,ActiveState,SubState,Type,After,Requires 2>/dev/null`, name)
		if propsOut, _, _, perr := exec.ExecContext(ctx, propsCmd); perr == nil {
			for _, prop := range strings.Split(strings.TrimSpace(propsOut), "\n") {
				if parts := strings.SplitN(prop, "=", 2); len(parts) == 2 {
					key, value := parts[0], parts[1]
					switch key {
					case "Description":
						svc.Description = value
					case "LoadState":
						svc.LoadState = value
					case "ActiveState":
						svc.ActiveState = value
					case "SubState":
						svc.SubState = value
					case "Type":
						svc.Type = value
					case "After":
						// Parse space-separated list, filter out non-service entries
						for _, dep := range strings.Fields(value) {
							if strings.HasSuffix(dep, ".service") {
								svc.DependsOn = append(svc.DependsOn, dep)
							}
						}
					case "Requires":
						for _, dep := range strings.Fields(value) {
							if strings.HasSuffix(dep, ".service") {
								// Avoid duplicates
								found := false
								for _, existing := range svc.DependsOn {
									if existing == dep {
										found = true
										break
									}
								}
								if !found {
									svc.DependsOn = append(svc.DependsOn, dep)
								}
							}
						}
					}
				}
			}
		}

		services = append(services, svc)
	}

	return services, nil
}

// --- DatabaseCollector ---

// DatabaseCollector detects running database instances (MySQL, PostgreSQL,
// MongoDB, Redis) by checking for running processes and listening ports.
type DatabaseCollector struct{}

func (c *DatabaseCollector) Name() string { return "database" }
func (c *DatabaseCollector) Timeout() time.Duration { return 30 * time.Second }

func (c *DatabaseCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	var databases []DatabaseInfo

	// Detect MySQL
	if db, err := detectDatabase(ctx, exec, "mysql", "mysqld", "mysql --version 2>/dev/null"); err == nil && db != nil {
		databases = append(databases, *db)
	}

	// Detect PostgreSQL
	if db, err := detectDatabase(ctx, exec, "postgresql", "postgres", "psql --version 2>/dev/null"); err == nil && db != nil {
		databases = append(databases, *db)
	}

	// Detect MongoDB
	if db, err := detectDatabase(ctx, exec, "mongodb", "mongod", "mongod --version 2>/dev/null | head -1"); err == nil && db != nil {
		databases = append(databases, *db)
	}

	// Detect Redis
	if db, err := detectDatabase(ctx, exec, "redis", "redis-server", "redis-server --version 2>/dev/null"); err == nil && db != nil {
		databases = append(databases, *db)
	}

	if len(databases) == 0 {
		return nil, nil
	}
	return databases, nil
}

// detectDatabase checks if a database is running and collects its info.
func detectDatabase(ctx context.Context, exec transport.SSHExecuter, dbType, processName, versionCmd string) (*DatabaseInfo, error) {
	// Check if the process is running
	cmd := fmt.Sprintf("pgrep -x '%s' > /dev/null 2>&1 && echo yes || echo no", processName)
	out, _, _, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) != "yes" {
		return nil, nil // Not running — not an error
	}

	info := &DatabaseInfo{
		Type:         dbType,
		ProcessName:  processName,
		Running:      true,
	}

	// Get version
	if verOut, _, _, verr := exec.ExecContext(ctx, versionCmd); verr == nil {
		info.Version = strings.TrimSpace(verOut)
	}

	// Get port from process
	portCmd := fmt.Sprintf(`ss -tlnp 2>/dev/null | grep '%s' | awk '{print $4}' | head -1`, processName)
	if portOut, _, _, perr := exec.ExecContext(ctx, portCmd); perr == nil {
		_, portStr := parseAddrPort(strings.TrimSpace(portOut))
		if port, perr := strconv.Atoi(portStr); perr == nil {
			info.Port = port
		}
	}

	// Get data directory (best-effort)
	switch dbType {
	case "mysql":
		if ddOut, _, _, ddErr := exec.ExecContext(ctx, `mysql -e "SELECT @@datadir" 2>/dev/null | tail -1`); ddErr == nil {
			info.DataDir = strings.TrimSpace(ddOut)
		}
	case "postgresql":
		if ddOut, _, _, ddErr := exec.ExecContext(ctx, `psql -c "SHOW data_directory" 2>/dev/null | tail -2 | head -1`); ddErr == nil {
			info.DataDir = strings.TrimSpace(ddOut)
		}
	case "mongodb":
		info.DataDir = "/data/db" // default
	case "redis":
		if ddOut, _, _, ddErr := exec.ExecContext(ctx, `redis-cli CONFIG GET dir 2>/dev/null | tail -1`); ddErr == nil {
			info.DataDir = strings.TrimSpace(ddOut)
		}
	}

	return info, nil
}

// --- NginxCollector ---

// NginxCollector collects Nginx configuration information.
type NginxCollector struct{}

func (c *NginxCollector) Name() string { return "nginx" }
func (c *NginxCollector) Timeout() time.Duration { return 30 * time.Second }

func (c *NginxCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	// Check if Nginx is installed
	if out, _, exitCode, _ := exec.ExecContext(ctx, "which nginx 2>/dev/null"); exitCode != 0 || strings.TrimSpace(out) == "" {
		return nil, nil // Nginx not installed — not an error
	}

	info := &NginxInfo{}

	// Version
	if out, _, _, err := exec.ExecContext(ctx, "nginx -v 2>&1"); err == nil {
		// Output: "nginx version: nginx/1.18.0"
		line := strings.TrimSpace(out)
		if idx := strings.LastIndex(line, "/"); idx >= 0 {
			info.Version = line[idx+1:]
		} else {
			info.Version = line
		}
	}

	// Virtual hosts — parse from nginx -T output
	if out, _, _, err := exec.ExecContext(ctx, "nginx -T 2>/dev/null"); err == nil {
		info.VHosts = parseNginxVHosts(out)
		info.Upstreams = parseNginxUpstreams(out)
	}

	// SSL certificates
	if out, _, _, err := exec.ExecContext(ctx, `find /etc/nginx -name "*.pem" -o -name "*.crt" 2>/dev/null | head -20`); err == nil {
		for _, certPath := range strings.Split(strings.TrimSpace(out), "\n") {
			certPath = strings.TrimSpace(certPath)
			if certPath == "" {
				continue
			}
			cert := parseSSLCert(ctx, exec, certPath)
			if cert != nil {
				info.SSLCerts = append(info.SSLCerts, *cert)
			}
		}
	}

	return info, nil
}

// parseNginxVHosts parses nginx -T output for server blocks.
func parseNginxVHosts(config string) []NginxVHost {
	var vhosts []NginxVHost
	lines := strings.Split(config, "\n")

	var current *NginxVHost
	var currentFile string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Track config file
		if strings.HasPrefix(line, "# configuration file") {
			if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
				currentFile = strings.Trim(strings.TrimSpace(parts[1]), ":")
			}
			continue
		}

		// Start of server block
		if strings.Contains(line, "server {") {
			current = &NginxVHost{ConfigFile: currentFile}
			continue
		}

		if current == nil {
			continue
		}

		// Parse directives
		if strings.Contains(line, "server_name") {
			current.ServerName = extractDirectiveValue(line, "server_name")
		}
		if strings.Contains(line, "listen") {
			current.Listen = extractDirectiveValue(line, "listen")
		}
		if strings.Contains(line, "root") && !strings.Contains(line, "#") {
			current.Root = extractDirectiveValue(line, "root")
		}
		if strings.Contains(line, "proxy_pass") {
			current.ProxyPass = extractDirectiveValue(line, "proxy_pass")
		}

		// End of server block
		if line == "}" {
			if current.ServerName != "" || current.Listen != "" {
				vhosts = append(vhosts, *current)
			}
			current = nil
		}
	}

	return vhosts
}

// parseNginxUpstreams parses nginx -T output for upstream blocks.
func parseNginxUpstreams(config string) []UpstreamConfig {
	var upstreams []UpstreamConfig
	lines := strings.Split(config, "\n")

	var current *UpstreamConfig

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Start of upstream block
		if strings.HasPrefix(line, "upstream ") && strings.HasSuffix(line, "{") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "upstream"), "{"))
			current = &UpstreamConfig{Name: name}
			continue
		}

		if current == nil {
			continue
		}

		// Parse server directive
		if strings.HasPrefix(line, "server ") {
			rest := strings.TrimPrefix(line, "server ")
			rest = strings.TrimSuffix(rest, ";")
			parts := strings.Fields(rest)
			if len(parts) >= 1 {
				srv := UpstreamServer{Address: parts[0]}
				for _, p := range parts[1:] {
					if strings.HasPrefix(p, "weight=") {
						srv.Weight, _ = strconv.Atoi(strings.TrimPrefix(p, "weight="))
					}
				}
				current.Servers = append(current.Servers, srv)
			}
		}

		// End of upstream block
		if line == "}" {
			if current != nil && len(current.Servers) > 0 {
				upstreams = append(upstreams, *current)
			}
			current = nil
		}
	}

	return upstreams
}

// extractDirectiveValue extracts the value from a directive like "server_name example.com;".
func extractDirectiveValue(line, directive string) string {
	idx := strings.Index(line, directive)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(directive):]
	rest = strings.TrimSpace(rest)
	rest = strings.TrimSuffix(rest, ";")
	return strings.TrimSpace(rest)
}

// parseSSLCert parses SSL certificate info using openssl.
func parseSSLCert(ctx context.Context, exec transport.SSHExecuter, certPath string) *SSLCert {
	cert := &SSLCert{Path: certPath}

	// Get expiry date and issuer
	cmd := fmt.Sprintf(`openssl x509 -in '%s' -noout -enddate -issuer 2>/dev/null`, certPath)
	out, _, _, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return nil
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "notAfter=") {
			dateStr := strings.TrimPrefix(line, "notAfter=")
			if t, err := time.Parse("Jan  2 15:04:05 2006 MST", dateStr); err == nil {
				cert.Expiry = t
				cert.DaysRemaining = int(time.Until(t).Hours() / 24)
			}
		}
		if strings.HasPrefix(line, "issuer=") {
			cert.Issuer = strings.TrimPrefix(line, "issuer=")
		}
	}

	// Try to extract domain from cert path
	parts := strings.Split(certPath, "/")
	filename := parts[len(parts)-1]
	cert.Domain = strings.TrimSuffix(strings.TrimSuffix(filename, ".pem"), ".crt")

	return cert
}
