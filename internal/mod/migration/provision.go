package migration

import (
	"context"
	"fmt"
	"log"
	"strings"

	"meshium/internal/shared"
)

// ProvisionEngine handles target server provisioning for migration.
// Installs Docker, Compose, Nginx, databases, and other dependencies.
type ProvisionEngine struct {
	targetSSH SSHExecuter
	repo      PipelineRepo
}

// NewProvisionEngine creates a new provision engine.
func NewProvisionEngine(targetSSH SSHExecuter, repo PipelineRepo) *ProvisionEngine {
	return &ProvisionEngine{
		targetSSH: targetSSH,
		repo:      repo,
	}
}

// ProvisionConfig configures target provisioning.
type ProvisionConfig struct {
	Components []string          `json:"components"`
	Versions   map[string]string `json:"versions,omitempty"`
	MigrationID int              `json:"migrationId"`
}

// Provision installs and configures all required components on the target.
func (e *ProvisionEngine) Provision(ctx context.Context, migrationID int, config ProvisionConfig) error {
	config.MigrationID = migrationID

	// Detect distro
	distro, err := e.detectDistro(ctx)
	if err != nil {
		return fmt.Errorf("detect distro: %w", err)
	}

	for _, component := range config.Components {
		if err := ctx.Err(); err != nil {
			return err
		}

		state := ProvisionState{
			MigrationID: migrationID,
			Component:   component,
		}
		stateID, err := e.repo.CreateProvisionState(ctx, state)
		if err != nil {
			return fmt.Errorf("create provision state for %s: %w", component, err)
		}

		version := config.Versions[component]
		installErr := e.installComponent(ctx, component, distro)
		if installErr != nil {
			if err := e.repo.UpdateProvisionState(ctx, stateID, false, false, false, version, installErr.Error()); err != nil {
				log.Printf("warning: failed to persist failed provision state for %s: %v", component, err)
			}
			return fmt.Errorf("provision %s: %w", component, installErr)
		}

		if err := e.repo.UpdateProvisionState(ctx, stateID, true, true, false, version, ""); err != nil {
			log.Printf("warning: failed to persist installed provision state for %s: %v", component, err)
		}

		verifyErr := e.verifyComponent(ctx, component)
		if verifyErr != nil {
			if err := e.repo.UpdateProvisionState(ctx, stateID, true, true, false, version, verifyErr.Error()); err != nil {
				log.Printf("warning: failed to persist verification failure for %s: %v", component, err)
			}
			return fmt.Errorf("verify %s: %w", component, verifyErr)
		}

		if err := e.repo.UpdateProvisionState(ctx, stateID, true, true, true, version, ""); err != nil {
			log.Printf("warning: failed to persist verified provision state for %s: %v", component, err)
		}
	}
	return nil
}

func (e *ProvisionEngine) installComponent(ctx context.Context, component, distro string) error {
	switch component {
	case "docker":
		return e.InstallDocker(ctx, distro)
	case "compose", "docker-compose":
		return e.InstallCompose(ctx, distro)
	case "nginx":
		return e.InstallNginx(ctx, distro)
	case "caddy":
		return e.InstallCaddy(ctx, distro)
	case "traefik":
		return e.InstallTraefik(ctx, distro)
	case "haproxy":
		return e.InstallHAProxy(ctx, distro)
	case "redis":
		return e.InstallRedis(ctx, distro)
	case "mysql", "mariadb":
		return e.InstallMySQL(ctx, distro)
	case "postgres", "postgresql":
		return e.InstallPostgreSQL(ctx, distro)
	case "mongodb":
		return e.InstallMongoDB(ctx, distro)
	case "nodejs":
		return e.InstallNodeJS(ctx, distro)
	case "ssh":
		return e.ConfigureSSH(ctx)
	case "firewall":
		return e.ConfigureFirewall(ctx, distro)
	case "swap":
		return e.ConfigureSwap(ctx)
	case "sysctl":
		return e.ConfigureSysctl(ctx)
	default:
		return fmt.Errorf("unknown component: %s", component)
	}
}

func (e *ProvisionEngine) verifyComponent(ctx context.Context, component string) error {
	var command string
	var expected []string

	switch component {
	case "docker":
		command = "docker version 2>&1"
		expected = []string{"Docker version"}
	case "compose", "docker-compose":
		command = "docker compose version 2>&1"
		expected = []string{"Docker Compose version"}
	case "nginx":
		command = "nginx -v 2>&1"
		expected = []string{"nginx version"}
	case "caddy":
		command = "caddy version 2>&1"
		expected = []string{"v"}
	case "traefik":
		command = "traefik version 2>&1"
		expected = []string{"Version:"}
	case "haproxy":
		command = "haproxy -v 2>&1"
		expected = []string{"HAProxy"}
	case "redis":
		command = "redis-cli ping 2>&1"
		expected = []string{"PONG"}
	case "mysql", "mariadb":
		command = "mysql --version 2>&1"
		expected = []string{"mysql", "MariaDB"}
	case "postgres", "postgresql":
		command = "psql --version 2>&1"
		expected = []string{"psql"}
	case "mongodb":
		command = "mongod --version 2>&1"
		expected = []string{"db version", "MongoDB"}
	case "nodejs":
		command = "node --version 2>&1"
		expected = []string{"v"}
	case "ssh":
		command = "sshd -T 2>&1"
		expected = []string{"port", "permitrootlogin"}
	case "firewall":
		command = "ufw status 2>&1 || iptables -L 2>&1"
		expected = []string{"Status", "Chain"}
	case "swap":
		command = "swapon --show 2>&1"
		expected = []string{"Filename", "/swap"}
	case "sysctl":
		command = "sysctl vm.swappiness 2>&1"
		expected = []string{"vm.swappiness"}
	default:
		command = fmt.Sprintf("which %s 2>&1", shared.ShellQuote(component))
	}

	output, _, _, err := e.targetSSH.ExecContext(ctx, command)
	if err != nil {
		return err
	}
	output = strings.TrimSpace(output)
	if len(expected) == 0 {
		if output == "" {
			return fmt.Errorf("verification returned no output for %s", component)
		}
		return nil
	}
	for _, token := range expected {
		if strings.Contains(strings.ToLower(output), strings.ToLower(token)) {
			return nil
		}
	}
	return fmt.Errorf("verification failed for %s: %s", component, output)
}

// detectDistro detects the Linux distribution on the target.
func (e *ProvisionEngine) detectDistro(ctx context.Context) (string, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "cat /etc/os-release 2>/dev/null || cat /etc/redhat-release 2>/dev/null || uname -s")
	if err != nil {
		return "", err
	}
	output = strings.ToLower(output)

	if strings.Contains(output, "ubuntu") || strings.Contains(output, "debian") {
		return "debian", nil
	}
	if strings.Contains(output, "centos") || strings.Contains(output, "rhel") || strings.Contains(output, "rocky") || strings.Contains(output, "almalinux") {
		return "rhel", nil
	}
	if strings.Contains(output, "alpine") {
		return "alpine", nil
	}
	if strings.Contains(output, "arch") {
		return "arch", nil
	}
	return "debian", nil // default to debian
}

// pkgInstall returns the package install command for the given distro.
func pkgInstall(distro string, packages ...string) string {
	pkgList := strings.Join(packages, " ")
	switch distro {
	case "debian":
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq %s 2>&1", pkgList)
	case "rhel":
		return fmt.Sprintf("dnf install -y %s 2>&1 || yum install -y %s 2>&1", pkgList, pkgList)
	case "alpine":
		return fmt.Sprintf("apk add --no-cache %s 2>&1", pkgList)
	case "arch":
		return fmt.Sprintf("pacman -Sy --noconfirm %s 2>&1", pkgList)
	default:
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq %s 2>&1", pkgList)
	}
}

// InstallDocker installs Docker on the target server.
func (e *ProvisionEngine) InstallDocker(ctx context.Context, distro string) error {
	// Check if already installed
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "docker --version 2>&1")
	if strings.Contains(output, "Docker version") {
		return nil // already installed
	}

	switch distro {
	case "debian":
		cmd := `DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates curl gnupg && install -m 0755 -d /etc/apt/keyrings && curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg && chmod a+r /etc/apt/keyrings/docker.gpg && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list && DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin 2>&1`
		_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
		return err
	case "rhel":
		cmd := `dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo && dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin 2>&1`
		_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
		return err
	default:
		cmd := `curl -fsSL https://get.docker.com | sh 2>&1`
		_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
		return err
	}
}

// InstallCompose installs Docker Compose on the target server.
func (e *ProvisionEngine) InstallCompose(ctx context.Context, distro string) error {
	// Docker Compose V2 is included with Docker CE (docker compose)
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "docker compose version 2>&1")
	if strings.Contains(output, "Docker Compose version") {
		return nil
	}
	// If not available, install standalone
	cmd := "mkdir -p /usr/local/lib/docker/cli-plugins && curl -SL https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64 -o /usr/local/lib/docker/cli-plugins/docker-compose && chmod +x /usr/local/lib/docker/cli-plugins/docker-compose 2>&1"
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}

// InstallNginx installs Nginx on the target server.
func (e *ProvisionEngine) InstallNginx(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "nginx -v 2>&1")
	if strings.Contains(output, "nginx version") {
		return nil
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, pkgInstall(distro, "nginx"))
	if err != nil {
		return err
	}
	_, _, _, err = e.targetSSH.ExecContext(ctx, "systemctl enable nginx && systemctl start nginx 2>&1")
	return err
}

// InstallCaddy installs Caddy on the target server.
func (e *ProvisionEngine) InstallCaddy(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "caddy version 2>&1")
	if strings.Contains(output, "v") {
		return nil
	}
	cmd := `apt install -y debian-keyring debian-archive-keyring apt-transport-https curl && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list && apt update && apt install caddy 2>&1`
	if distro == "rhel" {
		cmd = `dnf install -y yum-plugin-copr && dnf copr enable -y @caddy/caddy && dnf install -y caddy 2>&1`
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}

// InstallTraefik installs Traefik on the target server.
func (e *ProvisionEngine) InstallTraefik(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "traefik version 2>&1")
	if strings.Contains(output, "Version:") {
		return nil
	}
	cmd := `curl -sL https://github.com/traefik/traefik/releases/latest/download/traefik_v2.11_linux_amd64.tar.gz | tar xz -C /usr/local/bin traefik && chmod +x /usr/local/bin/traefik 2>&1`
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}

// InstallHAProxy installs HAProxy on the target server.
func (e *ProvisionEngine) InstallHAProxy(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "haproxy -v 2>&1")
	if strings.Contains(output, "HAProxy") {
		return nil
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, pkgInstall(distro, "haproxy"))
	if err != nil {
		return err
	}
	_, _, _, err = e.targetSSH.ExecContext(ctx, "systemctl enable haproxy && systemctl start haproxy 2>&1")
	return err
}

// InstallRedis installs Redis on the target server.
func (e *ProvisionEngine) InstallRedis(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "redis-server --version 2>&1")
	if strings.Contains(output, "Redis server") {
		return nil
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, pkgInstall(distro, "redis-server", "redis-tools"))
	if err != nil {
		return err
	}
	_, _, _, err = e.targetSSH.ExecContext(ctx, "systemctl enable redis-server && systemctl start redis-server 2>&1")
	return err
}

// InstallMySQL installs MySQL/MariaDB on the target server.
func (e *ProvisionEngine) InstallMySQL(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "mysql --version 2>&1")
	if strings.Contains(output, "mysql") || strings.Contains(output, "MariaDB") {
		return nil
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, pkgInstall(distro, "mysql-server"))
	if err != nil {
		return err
	}
	_, _, _, err = e.targetSSH.ExecContext(ctx, "systemctl enable mysql && systemctl start mysql 2>&1")
	return err
}

// InstallPostgreSQL installs PostgreSQL on the target server.
func (e *ProvisionEngine) InstallPostgreSQL(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "psql --version 2>&1")
	if strings.Contains(output, "psql") {
		return nil
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, pkgInstall(distro, "postgresql", "postgresql-contrib"))
	if err != nil {
		return err
	}
	_, _, _, err = e.targetSSH.ExecContext(ctx, "systemctl enable postgresql && systemctl start postgresql 2>&1")
	return err
}

// InstallMongoDB installs MongoDB on the target server.
func (e *ProvisionEngine) InstallMongoDB(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "mongod --version 2>&1")
	if strings.Contains(output, "db version") {
		return nil
	}
	cmd := `curl -fsSL https://www.mongodb.org/static/pgp/server-7.0.asc | gpg --dearmor -o /usr/share/keyrings/mongodb-server-7.0.gpg && echo "deb [ signed-by=/usr/share/keyrings/mongodb-server-7.0.gpg ] http://repo.mongodb.org/apt/debian bookworm/mongodb-org/7.0 main" > /etc/apt/sources.list.d/mongodb-org-7.0.list && apt-get update && apt-get install -y mongodb-org 2>&1`
	if distro == "rhel" {
		cmd = `echo '[mongodb-org-7.0] name=MongoDB Repository baseurl=https://repo.mongodb.org/yum/redhat/$releasever/mongodb-org/7.0/x86_64/ gpgcheck=1 enabled=1 gpgkey=https://www.mongodb.org/static/pgp/server-7.0.asc' > /etc/yum.repos.d/mongodb-org-7.0.repo && dnf install -y mongodb-org 2>&1`
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	if err != nil {
		return err
	}
	_, _, _, err = e.targetSSH.ExecContext(ctx, "systemctl enable mongod && systemctl start mongod 2>&1")
	return err
}

// InstallNodeJS installs Node.js on the target server.
func (e *ProvisionEngine) InstallNodeJS(ctx context.Context, distro string) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "node --version 2>&1")
	if strings.Contains(output, "v") {
		return nil
	}
	cmd := `curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs 2>&1`
	if distro == "rhel" {
		cmd = `curl -fsSL https://rpm.nodesource.com/setup_20.x | bash - && dnf install -y nodejs 2>&1`
	}
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}

// ConfigureSSH configures SSH on the target server.
func (e *ProvisionEngine) ConfigureSSH(ctx context.Context) error {
	cmds := []string{
		"mkdir -p ~/.ssh && chmod 700 ~/.ssh",
		"touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys",
	}
	for _, cmd := range cmds {
		if _, _, _, err := e.targetSSH.ExecContext(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}

// ConfigureFirewall configures the firewall on the target server.
func (e *ProvisionEngine) ConfigureFirewall(ctx context.Context, distro string) error {
	// Allow SSH, HTTP, HTTPS
	cmds := []string{
		"ufw allow 22/tcp 2>&1 || iptables -A INPUT -p tcp --dport 22 -j ACCEPT 2>&1",
		"ufw allow 80/tcp 2>&1 || iptables -A INPUT -p tcp --dport 80 -j ACCEPT 2>&1",
		"ufw allow 443/tcp 2>&1 || iptables -A INPUT -p tcp --dport 443 -j ACCEPT 2>&1",
	}
	for _, cmd := range cmds {
		e.targetSSH.ExecContext(ctx, cmd) // best effort
	}
	return nil
}

// ConfigureSwap configures swap space on the target server.
func (e *ProvisionEngine) ConfigureSwap(ctx context.Context) error {
	output, _, _, _ := e.targetSSH.ExecContext(ctx, "swapon --show 2>&1")
	if strings.Contains(output, "/swapfile") || strings.Contains(output, "NAME") {
		return nil // swap already configured
	}
	cmds := []string{
		"fallocate -l 2G /swapfile 2>&1",
		"chmod 600 /swapfile 2>&1",
		"mkswap /swapfile 2>&1",
		"swapon /swapfile 2>&1",
		"echo '/swapfile none swap sw 0 0' >> /etc/fstab",
	}
	for _, cmd := range cmds {
		if _, _, _, err := e.targetSSH.ExecContext(ctx, cmd); err != nil {
			return fmt.Errorf("configure swap: %w", err)
		}
	}
	return nil
}

// ConfigureSysctl configures kernel parameters on the target server.
func (e *ProvisionEngine) ConfigureSysctl(ctx context.Context) error {
	cmds := []string{
		"sysctl -w vm.swappiness=10 2>&1",
		"sysctl -w vm.overcommit_memory=1 2>&1",
		"sysctl -w net.core.somaxconn=65535 2>&1",
		"sysctl -w net.ipv4.tcp_max_syn_backlog=65535 2>&1",
		`echo 'vm.swappiness=10' >> /etc/sysctl.conf`,
		`echo 'vm.overcommit_memory=1' >> /etc/sysctl.conf`,
		`echo 'net.core.somaxconn=65535' >> /etc/sysctl.conf`,
	}
	for _, cmd := range cmds {
		e.targetSSH.ExecContext(ctx, cmd) // best effort
	}
	return nil
}

// ConfigureUser creates and configures a user on the target server.
func (e *ProvisionEngine) ConfigureUser(ctx context.Context, username string) error {
	cmd := fmt.Sprintf("id -u %s 2>/dev/null || useradd -m -s /bin/bash %s 2>&1", shared.ShellQuote(username), shared.ShellQuote(username))
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}

// ConfigureDirectory creates a directory on the target server.
func (e *ProvisionEngine) ConfigureDirectory(ctx context.Context, path string) error {
	cmd := fmt.Sprintf("mkdir -p %s 2>&1", shared.ShellQuote(path))
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}
