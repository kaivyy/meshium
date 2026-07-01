package sysupdate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

const (
	PackageTypeSecurity    = "security"
	PackageTypeBugfix      = "bugfix"
	PackageTypeEnhancement = "enhancement"
	PackageTypeNormal      = "normal"
)

var packageNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+:_-]*$`)

// Service manages package update discovery and package actions over SSH.
type Service struct {
	srvRepo server.Repo
	pool    *modssh.Pool
	authSvc transport.AESKeyProvider
	hosts   transport.HostKeyStore
}

// PackageUpdate describes an available system package update.
type PackageUpdate struct {
	Name             string `json:"name"`
	CurrentVersion   string `json:"currentVersion"`
	AvailableVersion string `json:"availableVersion"`
	Architecture     string `json:"architecture"`
	Repository       string `json:"repository"`
	Type             string `json:"type"`
	Security         bool   `json:"security"`
	Size             string `json:"size"`
}

// UpdateStatus captures the current update state for a server.
type UpdateStatus struct {
	ServerID        int             `json:"serverId"`
	Hostname        string          `json:"hostname"`
	PackageManager  string          `json:"packageManager"`
	LastChecked     string          `json:"lastChecked"`
	TotalUpdates    int             `json:"totalUpdates"`
	SecurityUpdates int             `json:"securityUpdates"`
	Packages        []PackageUpdate `json:"packages"`
}

// PackageInfo describes an installed package.
type PackageInfo struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description"`
	Size           string `json:"size"`
	Installed      bool   `json:"installed"`
	PackageManager string `json:"packageManager"`
}

// InstallRequest controls update installation.
type InstallRequest struct {
	SecurityOnly bool `json:"securityOnly"`
}

// InstallResult is returned after an install/remove action.
type InstallResult struct {
	ServerID       int    `json:"serverId"`
	PackageManager string `json:"packageManager"`
	SecurityOnly   bool   `json:"securityOnly,omitempty"`
	Updated        int    `json:"updated,omitempty"`
	PackageName    string `json:"packageName,omitempty"`
	Removed        bool   `json:"removed,omitempty"`
	LastChecked    string `json:"lastChecked"`
}

// NewService creates a new system update service.
func NewService(srvRepo server.Repo, pool *modssh.Pool, authSvc transport.AESKeyProvider, hosts transport.HostKeyStore) *Service {
	return &Service{
		srvRepo: srvRepo,
		pool:    pool,
		authSvc: authSvc,
		hosts:   hosts,
	}
}

func (s *Service) getSSHClient(ctx context.Context, serverID int) (transport.SSHExecuter, *server.Server, error) {
	if s.pool == nil {
		return nil, nil, errors.New("ssh pool is not configured")
	}

	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, nil, err
	}

	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, nil, errors.New("app is locked")
	}

	cfg := modssh.ServerConfig{
		ID:       srv.ID,
		Host:     srv.Host,
		Port:     srv.Port,
		Username: srv.Username,
	}

	if srv.Password != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.Password))
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt password: %w", err)
		}
		cfg.Password = string(decrypted)
	}
	if srv.SSHKey != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.SSHKey))
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt ssh key: %w", err)
		}
		cfg.PrivateKey = decrypted
	}
	if srv.Passphrase != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.Passphrase))
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt passphrase: %w", err)
		}
		cfg.Passphrase = string(decrypted)
	}
	if srv.BastionID != 0 {
		if bastion, err := s.srvRepo.GetByID(srv.BastionID); err == nil {
			cfg.Bastion = s.buildBastionConfig(bastion, aesKey)
		}
	}

	client, err := discovery.NewPoolAdapter(s.pool).Get(serverID, cfg, s.hosts.MakeHostKeyCallback(serverID))
	if err != nil {
		return nil, nil, err
	}
	return client, srv, nil
}

func (s *Service) buildBastionConfig(bastion *server.Server, aesKey []byte) *modssh.BastionConfig {
	if bastion == nil {
		return nil
	}

	port := bastion.Port
	if port == 0 {
		port = 22
	}

	cfg := &modssh.BastionConfig{
		Host:     bastion.Host,
		Port:     port,
		Username: bastion.Username,
	}

	if bastion.Password != "" {
		if decrypted, err := shared.Decrypt(aesKey, []byte(bastion.Password)); err == nil {
			cfg.Password = string(decrypted)
		}
	}
	if bastion.SSHKey != "" {
		if decrypted, err := shared.Decrypt(aesKey, []byte(bastion.SSHKey)); err == nil {
			cfg.PrivateKey = decrypted
		}
	}
	if bastion.Passphrase != "" {
		if decrypted, err := shared.Decrypt(aesKey, []byte(bastion.Passphrase)); err == nil {
			cfg.Passphrase = string(decrypted)
		}
	}

	return cfg
}

func (s *Service) exec(ctx context.Context, serverID int, command string) (transport.SSHExecuter, string, string, int, error) {
	client, _, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, "", "", -1, err
	}

	stdout, stderr, exitCode, err := client.ExecContext(ctx, command)
	return client, stdout, stderr, exitCode, err
}

func isValidPackageName(name string) bool {
	return packageNameRe.MatchString(name)
}

func validatePackageName(name string) error {
	if !isValidPackageName(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	return nil
}

func hasCommand(ctx context.Context, client transport.SSHExecuter, name string) bool {
	_, _, exitCode, err := client.ExecContext(ctx, fmt.Sprintf("command -v %s >/dev/null 2>&1", shared.ShellQuote(name)))
	return err == nil && exitCode == 0
}

func (s *Service) detectPackageManager(ctx context.Context, client transport.SSHExecuter) (string, error) {
	if info, err := migration.DetectDistro(ctx, client); err == nil {
		switch info.Family {
		case "debian":
			return "apt", nil
		case "arch":
			return "pacman", nil
		case "rhel":
			if hasCommand(ctx, client, "dnf") {
				return "dnf", nil
			}
			if hasCommand(ctx, client, "yum") {
				return "yum", nil
			}
			return "dnf", nil
		}
	}

	switch {
	case hasCommand(ctx, client, "apt-get"):
		return "apt", nil
	case hasCommand(ctx, client, "dnf"):
		return "dnf", nil
	case hasCommand(ctx, client, "yum"):
		return "yum", nil
	case hasCommand(ctx, client, "pacman"):
		return "pacman", nil
	default:
		return "", errors.New("unsupported package manager")
	}
}

func currentVersionForPackage(ctx context.Context, client transport.SSHExecuter, manager, name string) string {
	if !isValidPackageName(name) {
		return ""
	}

	var command string
	switch manager {
	case "apt":
		command = fmt.Sprintf("dpkg-query -W -f='${Version}' %s 2>/dev/null", shared.ShellQuote(name))
	case "dnf", "yum":
		command = fmt.Sprintf("rpm -q --qf '%%{VERSION}-%%{RELEASE}' %s 2>/dev/null", shared.ShellQuote(name))
	case "pacman":
		command = fmt.Sprintf("pacman -Q %s 2>/dev/null", shared.ShellQuote(name))
	default:
		return ""
	}

	stdout, _, exitCode, err := client.ExecContext(ctx, command)
	if err != nil || exitCode != 0 {
		return ""
	}
	stdout = strings.TrimSpace(stdout)
	if manager == "pacman" {
		fields := strings.Fields(stdout)
		if len(fields) >= 2 {
			return fields[1]
		}
	}
	return stdout
}

func parseAptUpdates(ctx context.Context, client transport.SSHExecuter, stdout string) []PackageUpdate {
	updates := make([]PackageUpdate, 0)
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Listing...") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		namePart := fields[0]
		name := namePart
		repo := ""
		if before, after, ok := strings.Cut(namePart, "/"); ok {
			name = before
			repo = after
		}

		current := ""
		if idx := strings.Index(line, "upgradable from:"); idx >= 0 {
			frag := line[idx+len("upgradable from:"):]
			frag = strings.TrimSpace(strings.TrimSuffix(frag, "]"))
			current = frag
		}
		if current == "" {
			current = currentVersionForPackage(ctx, client, "apt", name)
		}

		security := strings.Contains(strings.ToLower(repo), "security") || strings.Contains(strings.ToLower(line), "security")
		pkgType := PackageTypeNormal
		if security {
			pkgType = PackageTypeSecurity
		}

		updates = append(updates, PackageUpdate{
			Name:             name,
			CurrentVersion:   current,
			AvailableVersion: fields[1],
			Architecture:     fields[2],
			Repository:       repo,
			Type:             pkgType,
			Security:         security,
			Size:             "",
		})
	}
	return updates
}

func parseRpmUpdates(ctx context.Context, client transport.SSHExecuter, stdout, manager string) []PackageUpdate {
	updates := make([]PackageUpdate, 0)
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Last metadata") || strings.HasPrefix(line, "Loading mirror") || strings.HasPrefix(line, "Security:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if strings.Count(fields[0], ".") == 0 || strings.HasPrefix(fields[0], "Obsoleting") {
			continue
		}

		first := fields[0]
		name := first
		arch := ""
		if idx := strings.LastIndex(first, "."); idx > 0 {
			name = first[:idx]
			arch = first[idx+1:]
		}

		current := currentVersionForPackage(ctx, client, manager, name)
		pkgType := PackageTypeNormal
		updates = append(updates, PackageUpdate{
			Name:             name,
			CurrentVersion:   current,
			AvailableVersion: fields[1],
			Architecture:     arch,
			Repository:       fields[2],
			Type:             pkgType,
			Security:         false,
			Size:             "",
		})
	}
	return updates
}

func parsePacmanUpdates(ctx context.Context, client transport.SSHExecuter, stdout string) []PackageUpdate {
	updates := make([]PackageUpdate, 0)
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "error:") {
			continue
		}

		if strings.Contains(line, " -> ") {
			parts := strings.SplitN(line, " -> ", 2)
			left := strings.Fields(parts[0])
			right := strings.Fields(parts[1])
			if len(left) < 2 || len(right) < 1 {
				continue
			}
			name := left[0]
			current := left[1]
			available := right[0]
			updates = append(updates, PackageUpdate{
				Name:             name,
				CurrentVersion:   current,
				AvailableVersion: available,
				Architecture:     "",
				Repository:       "pacman",
				Type:             PackageTypeNormal,
				Security:         false,
				Size:             "",
			})
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		current := currentVersionForPackage(ctx, client, "pacman", name)
		available := fields[len(fields)-1]
		updates = append(updates, PackageUpdate{
			Name:             name,
			CurrentVersion:   current,
			AvailableVersion: available,
			Architecture:     "",
			Repository:       "pacman",
			Type:             PackageTypeNormal,
			Security:         false,
			Size:             "",
		})
	}
	return updates
}

func (s *Service) checkUpdates(ctx context.Context, serverID int) (*UpdateStatus, error) {
	client, srv, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	manager, err := s.detectPackageManager(ctx, client)
	if err != nil {
		return nil, err
	}

	var command string
	switch manager {
	case "apt":
		command = "apt list --upgradable 2>/dev/null"
	case "dnf":
		command = "dnf check-update 2>/dev/null"
	case "yum":
		command = "yum check-update 2>/dev/null"
	case "pacman":
		command = "pacman -Qu 2>/dev/null"
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager)
	}

	stdout, _, _, err := client.ExecContext(ctx, command)
	if err != nil {
		return nil, err
	}

	var updates []PackageUpdate
	switch manager {
	case "apt":
		updates = parseAptUpdates(ctx, client, stdout)
	case "dnf", "yum":
		updates = parseRpmUpdates(ctx, client, stdout, manager)
	case "pacman":
		updates = parsePacmanUpdates(ctx, client, stdout)
	}

	sort.Slice(updates, func(i, j int) bool { return updates[i].Name < updates[j].Name })

	securityCount := 0
	for _, update := range updates {
		if update.Security {
			securityCount++
		}
	}

	return &UpdateStatus{
		ServerID:        srv.ID,
		Hostname:        srv.Host,
		PackageManager:  manager,
		LastChecked:     time.Now().UTC().Format(time.RFC3339),
		TotalUpdates:    len(updates),
		SecurityUpdates: securityCount,
		Packages:        updates,
	}, nil
}

func (s *Service) InstallUpdates(ctx context.Context, serverID int, securityOnly bool) (*InstallResult, error) {
	status, err := s.checkUpdates(ctx, serverID)
	if err != nil {
		return nil, err
	}

	if len(status.Packages) == 0 {
		return &InstallResult{
			ServerID:       status.ServerID,
			PackageManager: status.PackageManager,
			SecurityOnly:   securityOnly,
			Updated:        0,
			LastChecked:    status.LastChecked,
		}, nil
	}

	command, err := installUpdatesCommand(status.PackageManager, securityOnly)
	if err != nil {
		return nil, err
	}

	client, _, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	_, stderr, exitCode, err := client.ExecContext(ctx, command)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = fmt.Sprintf("update install failed with exit code %d", exitCode)
		}
		return nil, errors.New(msg)
	}

	updated := status.TotalUpdates
	if securityOnly && status.SecurityUpdates > 0 {
		updated = status.SecurityUpdates
	}

	return &InstallResult{
		ServerID:       status.ServerID,
		PackageManager: status.PackageManager,
		SecurityOnly:   securityOnly,
		Updated:        updated,
		LastChecked:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func installUpdatesCommand(manager string, securityOnly bool) (string, error) {
	switch manager {
	case "apt":
		if securityOnly {
			return "DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y unattended-upgrades && unattended-upgrade -v", nil
		}
		return "DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y", nil
	case "dnf":
		if securityOnly {
			return "dnf upgrade -y --security", nil
		}
		return "dnf upgrade -y", nil
	case "yum":
		if securityOnly {
			return "yum update -y --security", nil
		}
		return "yum update -y", nil
	case "pacman":
		return "pacman -Syu --noconfirm", nil
	default:
		return "", fmt.Errorf("unsupported package manager: %s", manager)
	}
}

func (s *Service) InstallPackage(ctx context.Context, serverID int, packageName string) (*InstallResult, error) {
	if err := validatePackageName(packageName); err != nil {
		return nil, err
	}

	client, srv, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	manager := s.detectPackageManagerFromClient(ctx, client)
	command, err := installPackageCommand(manager, packageName)
	if err != nil {
		return nil, err
	}

	_, stderr, exitCode, err := client.ExecContext(ctx, command)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = fmt.Sprintf("install failed with exit code %d", exitCode)
		}
		return nil, errors.New(msg)
	}

	return &InstallResult{
		ServerID:       srv.ID,
		PackageManager: manager,
		PackageName:    packageName,
		LastChecked:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Service) RemovePackage(ctx context.Context, serverID int, packageName string) (*InstallResult, error) {
	if err := validatePackageName(packageName); err != nil {
		return nil, err
	}

	client, srv, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	manager := s.detectPackageManagerFromClient(ctx, client)
	command, err := removePackageCommand(manager, packageName)
	if err != nil {
		return nil, err
	}

	_, stderr, exitCode, err := client.ExecContext(ctx, command)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = fmt.Sprintf("remove failed with exit code %d", exitCode)
		}
		return nil, errors.New(msg)
	}

	return &InstallResult{
		ServerID:       srv.ID,
		PackageManager: manager,
		PackageName:    packageName,
		Removed:        true,
		LastChecked:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func installPackageCommand(manager, packageName string) (string, error) {
	if !isValidPackageName(packageName) {
		return "", fmt.Errorf("invalid package name %q", packageName)
	}

	pkg := shared.ShellQuote(packageName)
	switch manager {
	case "apt":
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get install -y %s", pkg), nil
	case "dnf":
		return fmt.Sprintf("dnf install -y %s", pkg), nil
	case "yum":
		return fmt.Sprintf("yum install -y %s", pkg), nil
	case "pacman":
		return fmt.Sprintf("pacman -S --noconfirm %s", pkg), nil
	default:
		return "", fmt.Errorf("unsupported package manager: %s", manager)
	}
}

func removePackageCommand(manager, packageName string) (string, error) {
	if !isValidPackageName(packageName) {
		return "", fmt.Errorf("invalid package name %q", packageName)
	}

	pkg := shared.ShellQuote(packageName)
	switch manager {
	case "apt":
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get remove -y %s", pkg), nil
	case "dnf":
		return fmt.Sprintf("dnf remove -y %s", pkg), nil
	case "yum":
		return fmt.Sprintf("yum remove -y %s", pkg), nil
	case "pacman":
		return fmt.Sprintf("pacman -R --noconfirm %s", pkg), nil
	default:
		return "", fmt.Errorf("unsupported package manager: %s", manager)
	}
}

func (s *Service) detectPackageManagerFromClient(ctx context.Context, client transport.SSHExecuter) string {
	manager, err := s.detectPackageManager(ctx, client)
	if err != nil {
		return "unknown"
	}
	return manager
}

func parseSizeToString(value string, manager string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}

	if manager == "apt" {
		// dpkg-query reports Installed-Size in KiB.
		n = n * 1024
	}
	return humanBytes(n)
}

func humanBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}

	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(bytes)
	unit := "B"
	for _, next := range units {
		value /= 1024
		unit = next
		if value < 1024 {
			break
		}
	}
	if unit == "KB" && value < 1 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func (s *Service) ListInstalledPackages(ctx context.Context, serverID int, filter string) ([]PackageInfo, error) {
	client, _, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	manager, err := s.detectPackageManager(ctx, client)
	if err != nil {
		return nil, err
	}

	var command string
	switch manager {
	case "apt":
		command = "dpkg-query -W -f='${Package}\t${Version}\t${Installed-Size}\t${binary:Summary}\n'"
	case "dnf", "yum":
		command = "rpm -qa --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{SIZE}\t%{SUMMARY}\n'"
	case "pacman":
		command = "pacman -Q"
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager)
	}

	if strings.TrimSpace(filter) != "" {
		command += " | grep -Fi -- " + shared.ShellQuote(filter)
	}

	stdout, _, _, err := client.ExecContext(ctx, command)
	if err != nil {
		return nil, err
	}

	packages := parseInstalledPackages(manager, stdout)
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages, nil
}

func parseInstalledPackages(manager, stdout string) []PackageInfo {
	packages := make([]PackageInfo, 0)
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			fields = strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
		}

		pkg := PackageInfo{
			Name:           fields[0],
			Version:        fields[1],
			Installed:      true,
			PackageManager: manager,
		}
		if len(fields) > 2 {
			pkg.Size = parseSizeToString(fields[2], manager)
		}
		if len(fields) > 3 {
			pkg.Description = strings.TrimSpace(fields[3])
		}
		packages = append(packages, pkg)
	}
	return packages
}

func (s *Service) GetPackageInfo(ctx context.Context, serverID int, packageName string) (*PackageInfo, error) {
	if err := validatePackageName(packageName); err != nil {
		return nil, err
	}

	packages, err := s.ListInstalledPackages(ctx, serverID, packageName)
	if err != nil {
		return nil, err
	}
	for _, pkg := range packages {
		if pkg.Name == packageName {
			result := pkg
			return &result, nil
		}
	}
	return nil, fmt.Errorf("package %q not found", packageName)
}

func (s *Service) CheckUpdates(ctx context.Context, serverID int) (*UpdateStatus, error) {
	return s.checkUpdates(ctx, serverID)
}

func (s *Service) ExecPackageAction(ctx context.Context, serverID int, packageName string, action string) (*InstallResult, error) {
	if action == "install" {
		return s.InstallPackage(ctx, serverID, packageName)
	}
	if action == "remove" {
		return s.RemovePackage(ctx, serverID, packageName)
	}
	return nil, fmt.Errorf("unsupported action: %s", action)
}

func (s *Service) ListPackageInfoFromStatus(ctx context.Context, serverID int) (*UpdateStatus, error) {
	return s.CheckUpdates(ctx, serverID)
}

func (s *Service) UpdatePackages(ctx context.Context, serverID int, req InstallRequest) (*InstallResult, error) {
	return s.InstallUpdates(ctx, serverID, req.SecurityOnly)
}

func (s *Service) PackageManager(ctx context.Context, serverID int) (string, error) {
	client, _, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return "", err
	}
	return s.detectPackageManager(ctx, client)
}

func (s *Service) GetServerID(ctx context.Context, serverID int) (int, error) {
	_, srv, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return 0, err
	}
	return srv.ID, nil
}
