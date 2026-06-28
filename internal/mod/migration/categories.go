package migration

import (
	"context"
	"encoding/json"
)

// CategoryData is a generic container for collected data.
// Each category module defines its own concrete data type and serializes to JSON.
type CategoryData struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// BackupData is a generic container for backup data.
type BackupData struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Collector reads data from the source server.
type Collector interface {
	Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error)
}

// Applier writes data to the target server.
type Applier interface {
	Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error)
	Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error
	Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error
}

// CategoryModule pairs a collector with an applier.
type CategoryModule struct {
	Name      string
	Collector Collector
	Applier   Applier
}

// CategoryRegistry holds all available category modules.
type CategoryRegistry struct {
	modules map[string]CategoryModule
}

// NewCategoryRegistry creates a registry with all 4 category modules.
func NewCategoryRegistry() *CategoryRegistry {
	r := &CategoryRegistry{modules: make(map[string]CategoryModule)}
	r.Register("packages", &PackagesCollector{}, &PackagesApplier{})
	r.Register("configs", &ConfigsCollector{}, &ConfigsApplier{})
	r.Register("services", &ServicesCollector{}, &ServicesApplier{})
	r.Register("users", &UsersCollector{}, &UsersApplier{})
	r.Register("docker", &DockerCollector{}, &DockerApplier{})
	return r
}

// Register adds a category module to the registry.
func (r *CategoryRegistry) Register(name string, c Collector, a Applier) {
	r.modules[name] = CategoryModule{Name: name, Collector: c, Applier: a}
}

// Get returns a category module by name.
func (r *CategoryRegistry) Get(name string) (CategoryModule, bool) {
	m, ok := r.modules[name]
	return m, ok
}

// Available returns the names of all registered categories.
func (r *CategoryRegistry) Available() []string {
	names := make([]string, 0, len(r.modules))
	for name := range r.modules {
		names = append(names, name)
	}
	return names
}
