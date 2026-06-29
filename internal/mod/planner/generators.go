package planner

import (
	"fmt"

	"meshium/internal/mod/discovery"
)

// StepGenerator generates a PlannedStep for a specific dependency graph node.
// Each workload type (Docker, Database, File, Nginx, Service) has its own generator.
type StepGenerator interface {
	// Generate creates a PlannedStep for the given graph node,
	// using the source and target snapshots for context.
	Generate(node discovery.DependencyNode, source, target *discovery.ServerSnapshot) (*PlannedStep, error)
}

// --- Docker Step Generator ---

// DockerStepGenerator generates steps for Docker containers.
// For each container, it generates two steps:
//   1. DockerImage: pull the image on the target
//   2. DockerVolume: transfer the container's volumes
//
// The generator returns the volume transfer step (the image step is
// generated as a dependency by the planner).
type DockerStepGenerator struct{}

// Generate creates a PlannedStep for a Docker container volume transfer.
func (g *DockerStepGenerator) Generate(node discovery.DependencyNode, source, target *discovery.ServerSnapshot) (*PlannedStep, error) {
	if source == nil || source.Docker == nil {
		return nil, fmt.Errorf("source snapshot has no Docker info")
	}

	// Find the container by name (node.ID is "container:<name>")
	containerName := node.Name
	var container *discovery.ContainerInfo
	for i := range source.Docker.Containers {
		if source.Docker.Containers[i].Name == containerName {
			container = &source.Docker.Containers[i]
			break
		}
	}
	if container == nil {
		return nil, fmt.Errorf("container %q not found in source snapshot", containerName)
	}

	// Build the config for the volume transfer step
	config := map[string]interface{}{
		"containerName": container.Name,
		"image":         container.Image,
		"volumes":       container.Volumes,
		"ports":         container.Ports,
		"networks":      container.Networks,
		"labels":        container.Labels,
		"state":         container.State,
	}

	// Find compose project if any
	for _, proj := range source.Docker.ComposeProjects {
		for _, svc := range proj.Services {
			if svc == container.Name {
				config["composeProject"] = proj.Name
				config["composeFile"] = proj.ConfigFiles
				break
			}
		}
	}

	return &PlannedStep{
		Name:       fmt.Sprintf("docker-volume:%s", container.Name),
		Type:       StepTypeDockerVolume,
		Reversible: true,
		Config:     config,
	}, nil
}

// GenerateImageStep creates a PlannedStep for pulling a Docker image on the target.
func (g *DockerStepGenerator) GenerateImageStep(container *discovery.ContainerInfo) *PlannedStep {
	return &PlannedStep{
		Name:       fmt.Sprintf("docker-image:%s", container.Image),
		Type:       StepTypeDockerImage,
		Reversible: true,
		Config: map[string]interface{}{
			"image":         container.Image,
			"containerName": container.Name,
		},
	}
}

// --- Database Step Generator ---

// DatabaseStepGenerator generates steps for database dump/transfer/restore.
type DatabaseStepGenerator struct{}

// Generate creates a PlannedStep for a database migration.
func (g *DatabaseStepGenerator) Generate(node discovery.DependencyNode, source, target *discovery.ServerSnapshot) (*PlannedStep, error) {
	if source == nil {
		return nil, fmt.Errorf("source snapshot is nil")
	}

	// Find the database by type (node.ID is "database:<type>")
	dbType := node.Name
	var db *discovery.DatabaseInfo
	for i := range source.Databases {
		if source.Databases[i].Type == dbType {
			db = &source.Databases[i]
			break
		}
	}
	if db == nil {
		return nil, fmt.Errorf("database %q not found in source snapshot", dbType)
	}

	// Build dump and restore commands based on database type
	dumpCmd, restoreCmd := databaseCommands(db.Type, db.Port)

	config := map[string]interface{}{
		"type":           db.Type,
		"version":        db.Version,
		"port":           db.Port,
		"dataDir":        db.DataDir,
		"sizeMB":         db.SizeMB,
		"running":        db.Running,
		"dumpCommand":    dumpCmd,
		"restoreCommand": restoreCmd,
	}

	return &PlannedStep{
		Name:       fmt.Sprintf("database:%s", db.Type),
		Type:       StepTypeDatabase,
		Reversible: true,
		Config:     config,
	}, nil
}

// databaseCommands returns the dump and restore commands for a database type.
func databaseCommands(dbType string, port int) (string, string) {
	switch dbType {
	case "mysql":
		return fmt.Sprintf("mysqldump --all-databases --single-transaction --host=127.0.0.1 --port=%d", port),
			fmt.Sprintf("mysql --host=127.0.0.1 --port=%d", port)
	case "postgresql":
		return fmt.Sprintf("pg_dumpall --host=127.0.0.1 --port=%d", port),
			fmt.Sprintf("psql --host=127.0.0.1 --port=%d", port)
	case "mongodb":
		return fmt.Sprintf("mongodump --host=127.0.0.1 --port=%d --archive", port),
			fmt.Sprintf("mongorestore --host=127.0.0.1 --port=%d --archive", port)
	case "redis":
		return "redis-cli BGSAVE && cat /var/lib/redis/dump.rdb",
			"cat > /var/lib/redis/dump.rdb && redis-cli SHUTDOWN NOSAVE || true"
	default:
		return fmt.Sprintf("echo 'Unknown database type: %s'", dbType),
			fmt.Sprintf("echo 'Unknown database type: %s'", dbType)
	}
}

// --- Nginx Step Generator ---

// NginxStepGenerator generates steps for Nginx configuration migration.
type NginxStepGenerator struct{}

// Generate creates a PlannedStep for an Nginx vhost migration.
func (g *NginxStepGenerator) Generate(node discovery.DependencyNode, source, target *discovery.ServerSnapshot) (*PlannedStep, error) {
	if source == nil || source.Nginx == nil {
		return nil, fmt.Errorf("source snapshot has no Nginx info")
	}

	// Find the vhost by server_name (node.ID is "nginx:<server_name>")
	serverName := node.Name
	var vhost *discovery.NginxVHost
	for i := range source.Nginx.VHosts {
		if source.Nginx.VHosts[i].ServerName == serverName {
			vhost = &source.Nginx.VHosts[i]
			break
		}
	}
	if vhost == nil {
		return nil, fmt.Errorf("nginx vhost %q not found in source snapshot", serverName)
	}

	config := map[string]interface{}{
		"serverName":  vhost.ServerName,
		"listen":      vhost.Listen,
		"root":        vhost.Root,
		"proxyPass":   vhost.ProxyPass,
		"configFile":  vhost.ConfigFile,
		"nginxVersion": source.Nginx.Version,
	}

	// Add SSL cert info if available
	for _, cert := range source.Nginx.SSLCerts {
		if cert.Domain == serverName {
			config["sslCertPath"] = cert.Path
			config["sslCertExpiry"] = cert.Expiry
			config["sslCertDaysRemaining"] = cert.DaysRemaining
			break
		}
	}

	return &PlannedStep{
		Name:       fmt.Sprintf("nginx:%s", vhost.ServerName),
		Type:       StepTypeNginx,
		Reversible: true,
		Config:     config,
	}, nil
}

// --- Service Step Generator ---

// ServiceStepGenerator generates steps for systemd service management.
type ServiceStepGenerator struct{}

// Generate creates a PlannedStep for enabling and starting a systemd service.
func (g *ServiceStepGenerator) Generate(node discovery.DependencyNode, source, target *discovery.ServerSnapshot) (*PlannedStep, error) {
	if source == nil {
		return nil, fmt.Errorf("source snapshot is nil")
	}

	// Find the service by name (node.ID is "service:<name>")
	svcName := node.Name
	var svc *discovery.SystemService
	for i := range source.Services {
		if source.Services[i].Name == svcName {
			svc = &source.Services[i]
			break
		}
	}
	if svc == nil {
		return nil, fmt.Errorf("service %q not found in source snapshot", svcName)
	}

	config := map[string]interface{}{
		"name":        svc.Name,
		"description": svc.Description,
		"type":        svc.Type,
		"dependsOn":   svc.DependsOn,
		"activeState": svc.ActiveState,
		"subState":    svc.SubState,
	}

	return &PlannedStep{
		Name:       fmt.Sprintf("service:%s", svc.Name),
		Type:       StepTypeService,
		Reversible: true,
		Config:     config,
	}, nil
}

// --- File Step Generator ---

// FileStepGenerator generates steps for file/directory transfers.
// This is used for non-Docker, non-config file paths (e.g., /var/www, /opt/app).
type FileStepGenerator struct {
	// Paths is the list of file/directory paths to transfer.
	Paths []string
}

// Generate creates a PlannedStep for a file transfer.
// Note: File steps are not generated from graph nodes — they are generated
// separately by the planner for custom paths. This method is provided for
// interface compliance but is not typically called.
func (g *FileStepGenerator) Generate(node discovery.DependencyNode, source, target *discovery.ServerSnapshot) (*PlannedStep, error) {
	return nil, fmt.Errorf("file steps are not generated from graph nodes")
}

// GenerateForPath creates a PlannedStep for a specific file/directory path.
func (g *FileStepGenerator) GenerateForPath(path string, source, target *discovery.ServerSnapshot) *PlannedStep {
	return &PlannedStep{
		Name:       fmt.Sprintf("file:%s", path),
		Type:       StepTypeFile,
		Reversible: true,
		Config: map[string]interface{}{
			"sourcePath":  path,
			"targetPath":  path,
			"isDirectory": true,
		},
	}
}

// --- Config Step Generator ---

// ConfigStepGenerator generates steps for configuration file transfers.
type ConfigStepGenerator struct {
	// Paths is the list of config paths to transfer (e.g., /etc/nginx, /etc/mysql).
	Paths []string
}

// GenerateForPath creates a PlannedStep for a config path.
func (g *ConfigStepGenerator) GenerateForPath(path string, source, target *discovery.ServerSnapshot) *PlannedStep {
	return &PlannedStep{
		Name:       fmt.Sprintf("config:%s", path),
		Type:       StepTypeConfig,
		Reversible: true,
		Config: map[string]interface{}{
			"sourcePath":  path,
			"targetPath":  path,
			"isDirectory": true,
		},
	}
}

// --- Generator Registry ---

// GeneratorRegistry holds step generators indexed by node type.
type GeneratorRegistry struct {
	generators map[string]StepGenerator
}

// NewGeneratorRegistry creates a registry with all default generators.
func NewGeneratorRegistry() *GeneratorRegistry {
	r := &GeneratorRegistry{generators: make(map[string]StepGenerator)}
	r.Register("container", &DockerStepGenerator{})
	r.Register("database", &DatabaseStepGenerator{})
	r.Register("nginx", &NginxStepGenerator{})
	r.Register("service", &ServiceStepGenerator{})
	return r
}

// Register adds a generator for a node type.
func (r *GeneratorRegistry) Register(nodeType string, gen StepGenerator) {
	r.generators[nodeType] = gen
}

// Get returns the generator for a node type.
func (r *GeneratorRegistry) Get(nodeType string) (StepGenerator, bool) {
	gen, ok := r.generators[nodeType]
	return gen, ok
}
