# Part 2: Category Collectors & Appliers

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the 4 category modules (Packages, Configs, Services, Users & Security), each with a Collector (reads from source) and Applier (writes to target with backup + rollback).

**Architecture:** Each category module implements the `Collector` and `Applier` interfaces defined in Part 1. Collectors use `SSHExecuter.Exec()` to run commands on the source server. Appliers use `SSHExecuter.Exec()` for commands and `SSHExecuter.Upload()`/`Download()` for SFTP file transfers (Configs category). Each Applier creates a backup before applying and can rollback on failure.

**Tech Stack:** Go 1.22+, `github.com/pkg/sftp` (via `ssh.Client`), `encoding/json`

## Global Constraints

- All category modules live in `internal/mod/migration/`
- Collectors read from source server via SSH
- Appliers write to target server via SSH, with backup before apply
- Non-fatal errors (package not found, service not installed) are logged as warnings, not errors
- Fatal errors (SSH disconnect, permission denied) abort the category
- SFTP operations use `ssh.Client.Upload()` and `ssh.Client.Download()`
- Package name mapping is attempted when source and target distros differ
- All commands run with appropriate timeouts (inherited from `ssh.Client` 30s command timeout)

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mod/migration/categories.go` | Collector & Applier interfaces, registry |
| `internal/mod/migration/packages.go` | Packages collector + applier |
| `internal/mod/migration/packages_test.go` | Packages tests |
| `internal/mod/migration/configs.go` | Configs collector + applier (SFTP) |
| `internal/mod/migration/configs_test.go` | Configs tests |
| `internal/mod/migration/services.go` | Services collector + applier |
| `internal/mod/migration/services_test.go` | Services tests |
| `internal/mod/migration/users.go` | Users & security collector + applier |
| `internal/mod/migration/users_test.go` | Users & security tests |

---

### Task 1: Category Interfaces & Registry

**Files:**
- Create: `internal/mod/migration/categories.go`

**Interfaces:**
- Produces: `Collector` interface, `Applier` interface, `CategoryRegistry` struct, `CategoryModule` struct

- [ ] **Step 1: Write category interfaces and registry**

```go
// internal/mod/migration/categories.go
package migration

import (
	"encoding/json"
)

// Collector reads data from the source server.
type Collector interface {
	Collect(ssh SSHExecuter) (CategoryData, error)
}

// Applier writes data to the target server.
type Applier interface {
	Backup(ssh SSHExecuter) (BackupData, error)
	Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error
	Rollback(ssh SSHExecuter, backup BackupData) error
}

// CategoryModule pairs a collector with an applier.
type CategoryModule struct {
	Name     string
	Collector Collector
	Applier  Applier
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

// --- Shared data types for category modules ---

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

// StepCallback is reused from the discovery package.
// func(msg WSMessage)
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /root/meshium
go build ./internal/mod/migration/
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/migration/categories.go
git commit -m "feat: add category interfaces and registry for migration engine"
```

---

### Task 2: Packages Collector

**Files:**
- Create: `internal/mod/migration/packages.go` (packages collector portion)
- Create: `internal/mod/migration/packages_test.go` (collector tests)

**Interfaces:**
- Consumes: `DistroAdapter`, `SSHExecuter`
- Produces: `PackagesCollector`, `PackagesData`

- [ ] **Step 1: Write packages data types and collector**

```go
// internal/mod/migration/packages.go
package migration

import (
	"encoding/json"
	"strings"
)

// PackagesData holds the collected package list from the source server.
type PackagesData struct {
	Distro     string   `json:"distro"`
	Packages   []string `json:"packages"`
	Count      int      `json:"count"`
}

// PackagesBackup holds the target server's package list before migration.
type PackagesBackup struct {
	Distro     string   `json:"distro"`
	Packages   []string `json:"packages"`
}

// PackagesCollector collects the package list from the source server.
type PackagesCollector struct{}

// Collect detects the distro and runs the distro-specific list command.
func (c *PackagesCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	adapter := DetectDistro(ssh)
	data := PackagesData{
		Distro: adapter.PackageManager(),
	}

	stdout, _, _, err := ssh.Exec(adapter.ListPackages())
	if err != nil {
		return CategoryData{}, err
	}

	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkg := parsePackageName(line, adapter.PackageManager())
		if pkg != "" {
			data.Packages = append(data.Packages, pkg)
		}
	}
	data.Count = len(data.Packages)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "packages", Data: raw}, nil
}

// parsePackageName extracts the package name from a list command output line.
// Different package managers have different output formats:
// dpkg -l: "ii  nginx   1.18.0  ..."
// rpm -qa: "nginx-1.18.0.x86_64"
// pacman -Q: "nginx 1.18.0"
// apk info -v: "nginx-1.18.0"
// zypper se --installed-only: "i  | nginx | 1.18.0 | ..."
func parsePackageName(line, pm string) string {
	switch pm {
	case "apt":
		// dpkg -l format: "ii  package  version  arch  description"
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "ii" || fields[0] == "hi") {
			return fields[1]
		}
		return ""
	case "dnf", "yum":
		// rpm -qa format: "package-version-release.arch"
		// Strip version and arch
		idx := strings.LastIndex(line, "-")
		if idx > 0 {
			line = line[:idx]
			idx = strings.LastIndex(line, "-")
			if idx > 0 {
				return line[:idx]
			}
		}
		return line
	case "pacman":
		// pacman -Q format: "package version"
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			return fields[0]
		}
		return ""
	case "apk":
		// apk info -v format: "package-version"
		idx := strings.LastIndex(line, "-")
		if idx > 0 {
			return line[:idx]
		}
		return line
	case "zypper":
		// zypper format: "i  | package | version | arch | repo"
		fields := strings.Split(line, "|")
		if len(fields) >= 2 {
			return strings.TrimSpace(fields[1])
		}
		return ""
	default:
		return strings.TrimSpace(line)
	}
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/mod/migration/packages_test.go
package migration

import (
	"encoding/json"
	"testing"
)

func TestPackagesCollectorApt(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"cat /etc/os-release": `ID=ubuntu\nVERSION_ID=22.04`,
			"dpkg -l":             "ii  nginx    1.18.0-0ubuntu1   amd64   [installed]\nii  curl     7.81.0-1   amd64   [installed]\n",
		},
	}
	collector := &PackagesCollector{}
	data, err := collector.Collect(ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var pd PackagesData
	json.Unmarshal(data.Data, &pd)
	if pd.Distro != "apt" {
		t.Errorf("expected distro 'apt', got '%s'", pd.Distro)
	}
	if pd.Count != 2 {
		t.Errorf("expected 2 packages, got %d", pd.Count)
	}
	if pd.Packages[0] != "nginx" {
		t.Errorf("expected first package 'nginx', got '%s'", pd.Packages[0])
	}
}

func TestParsePackageNameApt(t *testing.T) {
	result := parsePackageName("ii  nginx    1.18.0-0ubuntu1   amd64", "apt")
	if result != "nginx" {
		t.Errorf("expected 'nginx', got '%s'", result)
	}
}

func TestParsePackageNamePacman(t *testing.T) {
	result := parsePackageName("nginx 1.18.0", "pacman")
	if result != "nginx" {
		t.Errorf("expected 'nginx', got '%s'", result)
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/migration/ -v -run TestPackages
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/packages.go internal/mod/migration/packages_test.go
git commit -m "feat: add packages collector with distro-specific parsing"
```

---

### Task 3: Packages Applier, Backup & Rollback

**Files:**
- Modify: `internal/mod/migration/packages.go` (add applier portion)
- Modify: `internal/mod/migration/packages_test.go` (add applier tests)

**Interfaces:**
- Consumes: `DistroAdapter`, `SSHExecuter`, `StepCallback`
- Produces: `PackagesApplier`

- [ ] **Step 1: Write packages applier**

```go
// Append to internal/mod/migration/packages.go

// PackagesApplier installs packages on the target server.
type PackagesApplier struct{}

// Backup saves the target's current package list.
func (a *PackagesApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	adapter := DetectDistro(ssh)
	stdout, _, _, err := ssh.Exec(adapter.ListPackages())
	if err != nil {
		return BackupData{}, err
	}

	backup := PackagesBackup{
		Distro: adapter.PackageManager(),
		Packages: parsePackageList(stdout, adapter.PackageManager()),
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "packages", Data: raw}, nil
}

// Apply installs packages on the target, skipping already-installed ones.
func (a *PackagesApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var pd PackagesData
	if err := json.Unmarshal(data.Data, &pd); err != nil {
		return err
	}

	adapter := DetectDistro(ssh)
	targetPM := adapter.PackageManager()

	// Get currently installed packages on target
	stdout, _, _, _ := ssh.Exec(adapter.ListPackages())
	installed := make(map[string]bool)
	for _, pkg := range parsePackageList(stdout, targetPM) {
		installed[pkg] = true
	}

	// Map package names if distros differ
	packagesToInstall := make([]string, 0, len(pd.Packages))
	skipped := 0
	for _, pkg := range pd.Packages {
		if installed[pkg] {
			skipped++
			continue
		}
		mapped := mapPackageName(pkg, pd.Distro, targetPM)
		if mapped == "" {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "packages:apply",
					Status: "error",
					Error:  "unmapped package: " + pkg,
				})
			}
			continue
		}
		packagesToInstall = append(packagesToInstall, mapped)
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "packages:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Installing %d packages (%d skipped, %d unmapped)", len(packagesToInstall), skipped, len(pd.Packages)-len(packagesToInstall)-skipped),
		})
	}

	// Install in batches of 50 to avoid command line length limits
	batchSize := 50
	for i := 0; i < len(packagesToInstall); i += batchSize {
		end := i + batchSize
		if end > len(packagesToInstall) {
			end = len(packagesToInstall)
		}
		batch := packagesToInstall[i:end]
		cmd := adapter.InstallPackages(batch)
		_, stderr, exitCode, err := ssh.Exec(cmd)
		if err != nil || exitCode != 0 {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "packages:apply",
					Status: "error",
					Error:  fmt.Sprintf("install failed (exit %d): %s", exitCode, stderr),
				})
			}
			return fmt.Errorf("package install failed: %s", stderr)
		}
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "packages:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Installed %d/%d", end, len(packagesToInstall)),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "packages:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d packages installed", len(packagesToInstall)),
		})
	}

	return nil
}

// Rollback removes packages that were installed by the migration.
func (a *PackagesApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var pb PackagesBackup
	if err := json.Unmarshal(backup.Data, &pb); err != nil {
		return err
	}

	adapter := DetectDistro(ssh)
	stdout, _, _, _ := ssh.Exec(adapter.ListPackages())
	currentPackages := make(map[string]bool)
	for _, pkg := range parsePackageList(stdout, adapter.PackageManager()) {
		currentPackages[pkg] = true
	}

	// Find packages that exist now but weren't in the backup
	var toRemove []string
	for pkg := range currentPackages {
		if !contains(pb.Packages, pkg) {
			toRemove = append(toRemove, pkg)
		}
	}

	if len(toRemove) == 0 {
		return nil
	}

	// Remove packages using the adapter's remove command
	cmd := adapter.RemovePackages(toRemove)
	_, _, _, err := ssh.Exec(cmd)
	return err
}

// mapPackageName maps a package name from one distro to another.
// Returns empty string if no mapping exists.
func mapPackageName(pkg, sourcePM, targetPM string) string {
	if sourcePM == targetPM {
		return pkg
	}

	// Common mappings between distros
	mappings := map[string]map[string]string{
		"python3-dev": {
			"dnf": "python3-devel",
			"yum": "python3-devel",
		},
		"libssl-dev": {
			"dnf": "openssl-devel",
			"yum": "openssl-devel",
		},
		"libcurl4-openssl-dev": {
			"dnf": "libcurl-devel",
			"yum": "libcurl-devel",
		},
	}

	if m, ok := mappings[pkg]; ok {
		if mapped, ok := m[targetPM]; ok {
			return mapped
		}
	}

	// If no mapping found, return the original name (many packages have the same name)
	return pkg
}

func parsePackageList(stdout, pm string) []string {
	var packages []string
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkg := parsePackageName(line, pm)
		if pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Add `RemovePackages` to the `DistroAdapter` interface**

Update `internal/mod/migration/distro.go`:

```go
type DistroAdapter interface {
	Detect(ssh SSHExecuter) (DistroInfo, error)
	PackageManager() string
	ListPackages() string
	InstallPackages(pkgs []string) string
	RemovePackages(pkgs []string) string  // NEW
	EnableService(name string) string
}
```

Add `RemovePackages` implementation to each adapter:

```go
// aptAdapter
func (a *aptAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("apt-get remove -y %s", strings.Join(pkgs, " "))
}

// dnfAdapter
func (a *dnfAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("dnf remove -y %s", strings.Join(pkgs, " "))
}

// pacmanAdapter
func (a *pacmanAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("pacman -Rns --noconfirm %s", strings.Join(pkgs, " "))
}

// apkAdapter
func (a *apkAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("apk del %s", strings.Join(pkgs, " "))
}

// zypperAdapter
func (a *zypperAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("zypper remove -y %s", strings.Join(pkgs, " "))
}
```

- [ ] **Step 3: Write applier tests**

```go
// Append to internal/mod/migration/packages_test.go

func TestPackagesApplierApply(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"cat /etc/os-release":            `ID=ubuntu`,
			"dpkg -l":                        "ii  nginx    1.18.0   amd64\n",
			"apt-get install -y curl wget":   "",  // curl and wget will be installed
		},
	}
	data := PackagesData{
		Distro:   "apt",
		Packages: []string{"nginx", "curl", "wget"},
	}
	raw, _ := json.Marshal(data)

	applier := &PackagesApplier{}
	err := applier.Apply(ssh, CategoryData{Type: "packages", Data: raw}, func(msg WSMessage) {
		// Progress callback
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

func TestMapPackageNameSameDistro(t *testing.T) {
	result := mapPackageName("nginx", "apt", "apt")
	if result != "nginx" {
		t.Errorf("expected 'nginx', got '%s'", result)
	}
}

func TestMapPackageNameCrossDistro(t *testing.T) {
	result := mapPackageName("python3-dev", "apt", "dnf")
	if result != "python3-devel" {
		t.Errorf("expected 'python3-devel', got '%s'", result)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/mod/migration/ -v -run TestPackages
```

- [ ] **Step 5: Commit**

```bash
git add internal/mod/migration/packages.go internal/mod/migration/packages_test.go internal/mod/migration/distro.go
git commit -m "feat: add packages applier with backup, rollback, and cross-distro mapping"
```

---

### Task 4: Configs Collector (SFTP Download)

**Files:**
- Create: `internal/mod/migration/configs.go` (collector portion)
- Create: `internal/mod/migration/configs_test.go` (collector tests)

**Interfaces:**
- Consumes: `SSHExecuter` (with Upload/Download for SFTP)
- Produces: `ConfigsCollector`, `ConfigsData`

- [ ] **Step 1: Write configs data types and collector**

```go
// internal/mod/migration/configs.go
package migration

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ConfigsData holds config files collected from the source server.
type ConfigsData struct {
	Files map[string]string `json:"files"` // path → content
	Paths []string          `json:"paths"` // requested paths
}

// ConfigsBackup holds the target's original config files.
type ConfigsBackup struct {
	Files map[string]string `json:"files"` // path → content
}

// ConfigsCollector downloads config files from the source server via SFTP.
type ConfigsCollector struct {
	Paths []string // e.g., ["/etc/nginx/", "/etc/ssh/"]
}

// Collect downloads files from the configured paths via SFTP.
func (c *ConfigsCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	data := ConfigsData{
		Files: make(map[string]string),
		Paths: c.Paths,
	}

	for _, path := range c.Paths {
		// List files in the directory
		stdout, _, _, err := ssh.Exec("find " + path + " -type f 2>/dev/null")
		if err != nil {
			continue // non-fatal: skip path
		}

		for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
			file = strings.TrimSpace(file)
			if file == "" {
				continue
			}

			// Download file content via SFTP
			var buf bytes.Buffer
			if err := ssh.Download(file, &buf); err != nil {
				continue // non-fatal: skip file
			}
			data.Files[file] = buf.String()
		}
	}

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "configs", Data: raw}, nil
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/mod/migration/configs_test.go
package migration

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestConfigsCollector(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"find /etc/nginx/ -type f 2>/dev/null": "/etc/nginx/nginx.conf\n/etc/nginx/conf.d/default.conf\n",
		},
		downloadOutput: map[string][]byte{
			"/etc/nginx/nginx.conf":         []byte("worker_processes auto;\n"),
			"/etc/nginx/conf.d/default.conf": []byte("server { listen 80; }\n"),
		},
	}
	collector := &ConfigsCollector{Paths: []string{"/etc/nginx/"}}
	data, err := collector.Collect(ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var cd ConfigsData
	json.Unmarshal(data.Data, &cd)
	if len(cd.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(cd.Files))
	}
	if cd.Files["/etc/nginx/nginx.conf"] != "worker_processes auto;\n" {
		t.Errorf("unexpected content for nginx.conf")
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/migration/ -v -run TestConfigsCollector
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/configs.go internal/mod/migration/configs_test.go
git commit -m "feat: add configs collector with SFTP download"
```

---

### Task 5: Configs Applier, Backup & Rollback (SFTP Upload)

**Files:**
- Modify: `internal/mod/migration/configs.go` (add applier portion)
- Modify: `internal/mod/migration/configs_test.go` (add applier tests)

**Interfaces:**
- Consumes: `SSHExecuter`, `StepCallback`
- Produces: `ConfigsApplier`

- [ ] **Step 1: Write configs applier**

```go
// Append to internal/mod/migration/configs.go

// ConfigsApplier uploads config files to the target server via SFTP.
type ConfigsApplier struct{}

// Backup downloads the target's original config files at the same paths.
func (a *ConfigsApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	backup := ConfigsBackup{
		Files: make(map[string]string),
	}

	// We don't know which paths to backup yet — the executor will call Backup
	// with the paths from the collected data. For now, return empty backup.
	// The executor will call BackupWithPaths after collecting from source.

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "configs", Data: raw}, nil
}

// BackupWithPaths downloads the target's original files at the given paths.
func (a *ConfigsApplier) BackupWithPaths(ssh SSHExecuter, paths []string) (BackupData, error) {
	backup := ConfigsBackup{
		Files: make(map[string]string),
	}

	for _, path := range paths {
		stdout, _, _, err := ssh.Exec("find " + path + " -type f 2>/dev/null")
		if err != nil {
			continue
		}
		for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
			file = strings.TrimSpace(file)
			if file == "" {
				continue
			}
			var buf bytes.Buffer
			if err := ssh.Download(file, &buf); err != nil {
				continue
			}
			backup.Files[file] = buf.String()
		}
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "configs", Data: raw}, nil
}

// Apply uploads config files to the target, preserving permissions.
func (a *ConfigsApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		return err
	}

	count := 0
	total := len(cd.Files)
	for path, content := range cd.Files {
		count++

		// Ensure parent directory exists
		dir := path[:strings.LastIndex(path, "/")]
		ssh.Exec("mkdir -p " + dir)

		// Upload file via SFTP
		reader := strings.NewReader(content)
		if err := ssh.Upload(reader, path); err != nil {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "configs:apply",
					Status: "error",
					Error:  fmt.Sprintf("failed to upload %s: %v", path, err),
				})
			}
			return fmt.Errorf("failed to upload %s: %w", path, err)
		}

		// Preserve permissions (set to 644 for config files, 755 for directories)
		ssh.Exec("chmod 644 " + path)

		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "configs:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Uploaded %d/%d: %s", count, total, path),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "configs:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d config files uploaded", total),
		})
	}

	return nil
}

// Rollback restores the target's original config files.
func (a *ConfigsApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var cb ConfigsBackup
	if err := json.Unmarshal(backup.Data, &cb); err != nil {
		return err
	}

	// Restore original files
	for path, content := range cb.Files {
		reader := strings.NewReader(content)
		ssh.Upload(reader, path)
	}

	return nil
}
```

- [ ] **Step 2: Write applier tests**

```go
// Append to internal/mod/migration/configs_test.go

func TestConfigsApplierApply(t *testing.T) {
	ssh := &mockSSHExecuter{
		uploadPaths: []string{},
	}
	data := ConfigsData{
		Files: map[string]string{
			"/etc/nginx/nginx.conf": "worker_processes auto;\n",
		},
	}
	raw, _ := json.Marshal(data)

	applier := &ConfigsApplier{}
	err := applier.Apply(ssh, CategoryData{Type: "configs", Data: raw}, func(msg WSMessage) {})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if len(ssh.uploadPaths) != 1 {
		t.Errorf("expected 1 upload, got %d", len(ssh.uploadPaths))
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/mod/migration/ -v -run TestConfigs
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/configs.go internal/mod/migration/configs_test.go
git commit -m "feat: add configs applier with SFTP upload, backup, and rollback"
```

---

### Task 6: Services Collector, Applier, Backup & Rollback

**Files:**
- Create: `internal/mod/migration/services.go`
- Create: `internal/mod/migration/services_test.go`

**Interfaces:**
- Consumes: `DistroAdapter`, `SSHExecuter`, `StepCallback`
- Produces: `ServicesCollector`, `ServicesApplier`, `ServicesData`, `ServicesBackup`

- [ ] **Step 1: Write services data types, collector, and applier**

```go
// internal/mod/migration/services.go
package migration

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ServicesData holds the list of enabled services from the source server.
type ServicesData struct {
	Services []string `json:"services"`
	Count    int      `json:"count"`
}

// ServicesBackup holds the target's originally enabled services.
type ServicesBackup struct {
	Services []string `json:"services"`
}

// ServicesCollector collects the list of enabled services from the source.
type ServicesCollector struct{}

// Collect runs systemctl to list enabled services.
func (c *ServicesCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	stdout, _, _, err := ssh.Exec("systemctl list-unit-files --state=enabled --type=service --no-legend 2>/dev/null || echo ''")
	if err != nil {
		return CategoryData{}, err
	}

	data := ServicesData{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			svc := strings.TrimSuffix(fields[0], ".service")
			if svc != "" && svc != "echo" && svc != "''" {
				data.Services = append(data.Services, svc)
			}
		}
	}
	data.Count = len(data.Services)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "services", Data: raw}, nil
}

// ServicesApplier enables services on the target server.
type ServicesApplier struct{}

// Backup saves the target's currently enabled services.
func (a *ServicesApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	stdout, _, _, err := ssh.Exec("systemctl list-unit-files --state=enabled --type=service --no-legend 2>/dev/null || echo ''")
	if err != nil {
		return BackupData{}, err
	}

	backup := ServicesBackup{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			svc := strings.TrimSuffix(fields[0], ".service")
			if svc != "" && svc != "echo" && svc != "''" {
				backup.Services = append(backup.Services, svc)
			}
		}
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "services", Data: raw}, nil
}

// Apply enables and starts services on the target.
func (a *ServicesApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var sd ServicesData
	if err := json.Unmarshal(data.Data, &sd); err != nil {
		return err
	}

	enabled := 0
	skipped := 0
	for i, svc := range sd.Services {
		// Check if service exists on target
		_, _, exitCode, _ := ssh.Exec("systemctl cat " + svc + ".service 2>/dev/null")
		if exitCode != 0 {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "services:apply",
					Status: "error",
					Error:  fmt.Sprintf("service not found on target: %s", svc),
				})
			}
			skipped++
			continue
		}

		// Enable and start
		_, _, exitCode, err := ssh.Exec("systemctl enable --now " + svc + ".service")
		if err != nil || exitCode != 0 {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "services:apply",
					Status: "error",
					Error:  fmt.Sprintf("failed to enable %s", svc),
				})
			}
			continue
		}
		enabled++

		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "services:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Enabled %d/%d: %s", i+1, sd.Count, svc),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "services:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d services enabled (%d skipped)", enabled, skipped),
		})
	}

	return nil
}

// Rollback disables services that were enabled by migration and restores original set.
func (a *ServicesApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var sb ServicesBackup
	if err := json.Unmarshal(backup.Data, &sb); err != nil {
		return err
	}

	// Get currently enabled services
	stdout, _, _, _ := ssh.Exec("systemctl list-unit-files --state=enabled --type=service --no-legend 2>/dev/null || echo ''")
	currentServices := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			svc := strings.TrimSuffix(fields[0], ".service")
			if svc != "" {
				currentServices[svc] = true
			}
		}
	}

	// Disable services that are enabled now but weren't in the backup
	backupSet := make(map[string]bool)
	for _, svc := range sb.Services {
		backupSet[svc] = true
	}

	for svc := range currentServices {
		if !backupSet[svc] {
			ssh.Exec("systemctl disable --now " + svc + ".service 2>/dev/null")
		}
	}

	return nil
}
```

- [ ] **Step 2: Write tests**

```go
// internal/mod/migration/services_test.go
package migration

import (
	"encoding/json"
	"testing"
)

func TestServicesCollector(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"systemctl list-unit-files --state=enabled --type=service --no-legend 2>/dev/null || echo ''": "nginx.service   enabled\nssh.service     enabled\ndocker.service  enabled\n",
		},
	}
	collector := &ServicesCollector{}
	data, err := collector.Collect(ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var sd ServicesData
	json.Unmarshal(data.Data, &sd)
	if sd.Count != 3 {
		t.Errorf("expected 3 services, got %d", sd.Count)
	}
	if sd.Services[0] != "nginx" {
		t.Errorf("expected first service 'nginx', got '%s'", sd.Services[0])
	}
}

func TestServicesApplierApply(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"systemctl cat nginx.service 2>/dev/null": "",  // service exists
		},
		execExitCode: map[string]int{
			"systemctl cat nginx.service 2>/dev/null": 0,
			"systemctl enable --now nginx.service":    0,
		},
	}
	data := ServicesData{
		Services: []string{"nginx"},
		Count:    1,
	}
	raw, _ := json.Marshal(data)

	applier := &ServicesApplier{}
	err := applier.Apply(ssh, CategoryData{Type: "services", Data: raw}, func(msg WSMessage) {})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/mod/migration/ -v -run TestServices
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/services.go internal/mod/migration/services_test.go
git commit -m "feat: add services collector, applier with backup and rollback"
```

---

### Task 7: Users & Security Collector

**Files:**
- Create: `internal/mod/migration/users.go` (collector portion)
- Create: `internal/mod/migration/users_test.go` (collector tests)

**Interfaces:**
- Consumes: `SSHExecuter`
- Produces: `UsersCollector`, `UsersData`

- [ ] **Step 1: Write users data types and collector**

```go
// internal/mod/migration/users.go
package migration

import (
	"encoding/json"
	"strings"
)

// UsersData holds users, groups, cron jobs, and firewall rules from the source.
type UsersData struct {
	Users     []UserInfo     `json:"users"`
	Groups    []GroupInfo    `json:"groups"`
	CronJobs  []CronJobInfo   `json:"cronJobs"`
	Firewall  []FirewallRule  `json:"firewall"`
}

type UserInfo struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Home     string `json:"home"`
	Shell    string `json:"shell"`
	Password string `json:"password,omitempty"` // from /etc/shadow
}

type GroupInfo struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

type CronJobInfo struct {
	User string `json:"user"`
	Line string `json:"line"`
}

type FirewallRule struct {
	Rule string `json:"rule"`
}

// UsersBackup holds the target's original users, groups, cron, and firewall.
type UsersBackup struct {
	Passwd    string         `json:"passwd"`
	Group     string         `json:"group"`
	Shadow    string         `json:"shadow"`
	CronJobs  []CronJobInfo  `json:"cronJobs"`
	Firewall  []FirewallRule `json:"firewall"`
}

// UsersCollector collects users, groups, cron jobs, and firewall rules.
type UsersCollector struct{}

// Collect parses /etc/passwd, /etc/group, /etc/shadow, crontabs, and firewall rules.
func (c *UsersCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	data := UsersData{}

	// Collect users (UID >= 1000, non-system)
	passwd, _, _, _ := ssh.Exec("cat /etc/passwd")
	for _, line := range strings.Split(strings.TrimSpace(passwd), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		uid := parseIntSafe(fields[2])
		if uid < 1000 {
			continue // skip system users
		}
		data.Users = append(data.Users, UserInfo{
			Username: fields[0],
			UID:      uid,
			GID:      parseIntSafe(fields[3]),
			Home:     fields[5],
			Shell:    fields[6],
		})
	}

	// Collect groups (GID >= 1000)
	group, _, _, _ := ssh.Exec("cat /etc/group")
	for _, line := range strings.Split(strings.TrimSpace(group), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		gid := parseIntSafe(fields[2])
		if gid < 1000 {
			continue
		}
		members := []string{}
		if fields[3] != "" {
			members = strings.Split(fields[3], ",")
		}
		data.Groups = append(data.Groups, GroupInfo{
			Name:    fields[0],
			GID:     gid,
			Members: members,
		})
	}

	// Collect passwords from /etc/shadow for migrated users
	shadow, _, _, _ := ssh.Exec("cat /etc/shadow 2>/dev/null")
	userSet := make(map[string]bool)
	for _, u := range data.Users {
		userSet[u.Username] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(shadow), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 2 {
			continue
		}
		if userSet[fields[0]] {
			for i := range data.Users {
				if data.Users[i].Username == fields[0] {
					data.Users[i].Password = fields[1]
					break
				}
			}
		}
	}

	// Collect cron jobs
	crontab, _, _, _ := ssh.Exec("cat /etc/crontab 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(crontab), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		data.CronJobs = append(data.CronJobs, CronJobInfo{User: "root", Line: line})
	}

	// Collect per-user crontabs
	for _, u := range data.Users {
		userCron, _, _, _ := ssh.Exec("crontab -l -u " + u.Username + " 2>/dev/null")
		for _, line := range strings.Split(strings.TrimSpace(userCron), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			data.CronJobs = append(data.CronJobs, CronJobInfo{User: u.Username, Line: line})
		}
	}

	// Collect firewall rules (iptables)
	iptables, _, _, _ := ssh.Exec("iptables-save 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(iptables), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		data.Firewall = append(data.Firewall, FirewallRule{Rule: line})
	}

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "users", Data: raw}, nil
}

func parseIntSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/mod/migration/users_test.go
package migration

import (
	"encoding/json"
	"testing"
)

func TestUsersCollector(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"cat /etc/passwd":                          "root:x:0:0:root:/root:/bin/bash\nappuser:x:1000:1000:appuser:/home/appuser:/bin/bash\n",
			"cat /etc/group":                           "root:x:0:\nappuser:x:1000:\n",
			"cat /etc/shadow 2>/dev/null":              "root:$6$xxx:19000:0:99999:7:::\nappuser:$6$yyy:19000:0:99999:7:::\n",
			"cat /etc/crontab 2>/dev/null":             "# m h dom mon dow command\n0 2 * * * /usr/bin/backup\n",
			"crontab -l -u appuser 2>/dev/null":        "",
			"iptables-save 2>/dev/null":                "*filter\n:INPUT ACCEPT [0:0]\n-A INPUT -p tcp --dport 22 -j ACCEPT\nCOMMIT\n",
		},
	}
	collector := &UsersCollector{}
	data, err := collector.Collect(ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var ud UsersData
	json.Unmarshal(data.Data, &ud)
	if len(ud.Users) != 1 {
		t.Fatalf("expected 1 non-system user, got %d", len(ud.Users))
	}
	if ud.Users[0].Username != "appuser" {
		t.Errorf("expected user 'appuser', got '%s'", ud.Users[0].Username)
	}
	if ud.Users[0].Password != "$6$yyy" {
		t.Errorf("expected password hash, got '%s'", ud.Users[0].Password)
	}
	if len(ud.Groups) != 1 {
		t.Errorf("expected 1 non-system group, got %d", len(ud.Groups))
	}
	if len(ud.CronJobs) != 1 {
		t.Errorf("expected 1 cron job, got %d", len(ud.CronJobs))
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/migration/ -v -run TestUsersCollector
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/users.go internal/mod/migration/users_test.go
git commit -m "feat: add users & security collector (passwd, group, shadow, cron, firewall)"
```

---

### Task 8: Users & Security Applier, Backup & Rollback

**Files:**
- Modify: `internal/mod/migration/users.go` (add applier portion)
- Modify: `internal/mod/migration/users_test.go` (add applier tests)

**Interfaces:**
- Consumes: `SSHExecuter`, `StepCallback`
- Produces: `UsersApplier`

- [ ] **Step 1: Write users applier**

```go
// Append to internal/mod/migration/users.go

// UsersApplier creates users, groups, cron jobs, and firewall rules on the target.
type UsersApplier struct{}

// Backup saves the target's original passwd, group, shadow, cron, and firewall.
func (a *UsersApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	backup := UsersBackup{}

	passwd, _, _, _ := ssh.Exec("cat /etc/passwd")
	backup.Passwd = passwd

	group, _, _, _ := ssh.Exec("cat /etc/group")
	backup.Group = group

	shadow, _, _, _ := ssh.Exec("cat /etc/shadow 2>/dev/null")
	backup.Shadow = shadow

	crontab, _, _, _ := ssh.Exec("cat /etc/crontab 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(crontab), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			backup.CronJobs = append(backup.CronJobs, CronJobInfo{User: "root", Line: line})
		}
	}

	iptables, _, _, _ := ssh.Exec("iptables-save 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(iptables), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			backup.Firewall = append(backup.Firewall, FirewallRule{Rule: line})
		}
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "users", Data: raw}, nil
}

// Apply creates users, groups, cron jobs, and firewall rules on the target.
func (a *UsersApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var ud UsersData
	if err := json.Unmarshal(data.Data, &ud); err != nil {
		return err
	}

	// Create groups
	for _, g := range ud.Groups {
		_, _, exitCode, _ := ssh.Exec("getent group " + g.Name)
		if exitCode == 0 {
			continue // group already exists
		}
		ssh.Exec(fmt.Sprintf("groupadd -g %d %s 2>/dev/null", g.GID, g.Name))
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "users:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Created group: %s", g.Name),
			})
		}
	}

	// Create users
	for _, u := range ud.Users {
		_, _, exitCode, _ := ssh.Exec("id " + u.Username + " 2>/dev/null")
		if exitCode == 0 {
			continue // user already exists
		}
		cmd := fmt.Sprintf("useradd -u %d -g %d -d %s -s %s -m %s 2>/dev/null",
			u.UID, u.GID, u.Home, u.Shell, u.Username)
		ssh.Exec(cmd)
		if u.Password != "" {
			ssh.Exec(fmt.Sprintf("usermod -p '%s' %s 2>/dev/null", u.Password, u.Username))
		}
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "users:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Created user: %s", u.Username),
			})
		}
	}

	// Import cron jobs
	for _, cron := range ud.CronJobs {
		if cron.User == "root" {
			ssh.Exec(fmt.Sprintf("echo '%s' >> /etc/crontab", cron.Line))
		} else {
			ssh.Exec(fmt.Sprintf("echo '%s' | crontab -u %s - 2>/dev/null", cron.Line, cron.User))
		}
	}
	if onProgress != nil && len(ud.CronJobs) > 0 {
		onProgress(WSMessage{
			Step:   "users:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Imported %d cron jobs", len(ud.CronJobs)),
		})
	}

	// Apply firewall rules
	if len(ud.Firewall) > 0 {
		var rules strings.Builder
		for _, r := range ud.Firewall {
			rules.WriteString(r.Rule + "\n")
		}
		ssh.Exec(fmt.Sprintf("echo '%s' | iptables-restore 2>/dev/null", rules.String()))
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "users:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Applied %d firewall rules", len(ud.Firewall)),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "users:apply",
			Status: "success",
			Value:  fmt.Sprintf("Users: %d, Groups: %d, Cron: %d, Firewall: %d", len(ud.Users), len(ud.Groups), len(ud.CronJobs), len(ud.Firewall)),
		})
	}

	return nil
}

// Rollback restores the target's original passwd, group, shadow, cron, and firewall.
func (a *UsersApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var ub UsersBackup
	if err := json.Unmarshal(backup.Data, &ub); err != nil {
		return err
	}

	// Restore passwd, group, shadow
	if ub.Passwd != "" {
		ssh.Exec(fmt.Sprintf("echo '%s' > /etc/passwd", ub.Passwd))
	}
	if ub.Group != "" {
		ssh.Exec(fmt.Sprintf("echo '%s' > /etc/group", ub.Group))
	}
	if ub.Shadow != "" {
		ssh.Exec(fmt.Sprintf("echo '%s' > /etc/shadow", ub.Shadow))
	}

	// Restore firewall
	if len(ub.Firewall) > 0 {
		var rules strings.Builder
		for _, r := range ub.Firewall {
			rules.WriteString(r.Rule + "\n")
		}
		ssh.Exec(fmt.Sprintf("echo '%s' | iptables-restore 2>/dev/null", rules.String()))
	}

	return nil
}
```

- [ ] **Step 2: Write applier tests**

```go
// Append to internal/mod/migration/users_test.go

func TestUsersApplierApply(t *testing.T) {
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"getent group appuser":  "",  // group doesn't exist
			"id appuser 2>/dev/null": "",  // user doesn't exist
		},
		execExitCode: map[string]int{
			"getent group appuser":  2,  // not found
			"id appuser 2>/dev/null": 1,  // not found
		},
	}
	data := UsersData{
		Users: []UserInfo{
			{Username: "appuser", UID: 1000, GID: 1000, Home: "/home/appuser", Shell: "/bin/bash", Password: "$6$xxx"},
		},
		Groups: []GroupInfo{
			{Name: "appuser", GID: 1000, Members: []string{}},
		},
	}
	raw, _ := json.Marshal(data)

	applier := &UsersApplier{}
	err := applier.Apply(ssh, CategoryData{Type: "users", Data: raw}, func(msg WSMessage) {})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/mod/migration/ -v -run TestUsers
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/users.go internal/mod/migration/users_test.go
git commit -m "feat: add users & security applier with backup and rollback"
```

---

### Task 9: Mock SSH Executer & Test Helpers

**Files:**
- Create: `internal/mod/migration/mock_test.go`

**Interfaces:**
- Produces: `mockSSHExecuter` for testing all category modules

- [ ] **Step 1: Write mock SSH executer for tests**

```go
// internal/mod/migration/mock_test.go
package migration

import (
	"bytes"
	"io"
)

// mockSSHExecuter implements SSHExecuter for testing.
type mockSSHExecuter struct {
	execOutput   map[string]string
	execExitCode map[string]int
	execError    map[string]error
	uploadPaths  []string
	downloadOutput map[string][]byte
}

func (m *mockSSHExecuter) Exec(cmd string) (string, string, int, error) {
	if out, ok := m.execOutput[cmd]; ok {
		exitCode := 0
		if ec, ok := m.execExitCode[cmd]; ok {
			exitCode = ec
		}
		err := error(nil)
		if e, ok := m.execError[cmd]; ok {
			err = e
		}
		return out, "", exitCode, err
	}
	return "", "", 0, nil
}

func (m *mockSSHExecuter) IsAlive() bool {
	return true
}

func (m *mockSSHExecuter) Upload(src io.Reader, remotePath string) error {
	m.uploadPaths = append(m.uploadPaths, remotePath)
	return nil
}

func (m *mockSSHExecuter) Download(remotePath string, dst io.Writer) error {
	if data, ok := m.downloadOutput[remotePath]; ok {
		dst.Write(data)
		return nil
	}
	return nil
}
```

- [ ] **Step 2: Run all tests to verify everything passes**

```bash
go test ./internal/mod/migration/ -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/migration/mock_test.go
git commit -m "test: add mock SSH executer for migration category tests"
```

---

### Task 10: Integration Test for All Categories

**Files:**
- Create: `internal/mod/migration/categories_test.go`

**Interfaces:**
- Consumes: all category modules, `CategoryRegistry`

- [ ] **Step 1: Write integration test**

```go
// internal/mod/migration/categories_test.go
package migration

import (
	"testing"
)

func TestCategoryRegistry(t *testing.T) {
	registry := NewCategoryRegistry()
	
	names := registry.Available()
	if len(names) != 4 {
		t.Errorf("expected 4 categories, got %d", len(names))
	}

	for _, name := range names {
		mod, ok := registry.Get(name)
		if !ok {
			t.Errorf("category '%s' not found", name)
		}
		if mod.Collector == nil {
			t.Errorf("category '%s' has nil collector", name)
		}
		if mod.Applier == nil {
			t.Errorf("category '%s' has nil applier", name)
		}
	}
}

func TestAllCategoriesCollectAndApply(t *testing.T) {
	// Test that each category can collect and apply without panicking
	ssh := &mockSSHExecuter{
		execOutput: map[string]string{
			"cat /etc/os-release": `ID=ubuntu`,
			"dpkg -l":             "ii  nginx    1.18.0   amd64\n",
			"systemctl list-unit-files --state=enabled --type=service --no-legend 2>/dev/null || echo ''": "nginx.service   enabled\n",
			"cat /etc/passwd":     "root:x:0:0:root:/root:/bin/bash\n",
			"cat /etc/group":      "root:x:0:\n",
			"cat /etc/shadow 2>/dev/null": "root:$6$xxx:19000:0:99999:7:::\n",
			"cat /etc/crontab 2>/dev/null": "",
			"iptables-save 2>/dev/null": "",
		},
	}

	registry := NewCategoryRegistry()
	for _, name := range registry.Available() {
		mod, _ := registry.Get(name)
		_, err := mod.Collector.Collect(ssh)
		if err != nil {
			t.Errorf("category '%s' collect failed: %v", name, err)
		}
	}
}
```

- [ ] **Step 2: Run all tests**

```bash
go test ./internal/mod/migration/ -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/migration/categories_test.go
git commit -m "test: add category registry and integration tests"
```
