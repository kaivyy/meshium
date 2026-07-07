package ai

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// ChatMessage represents a chat transcript item.
type ChatMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

// ChatRequest is the request payload for the assistant chat endpoint.
type ChatRequest struct {
	Message  string        `json:"message"`
	ServerID int           `json:"serverId,omitempty"`
	History  []ChatMessage `json:"history,omitempty"`
}

// ChatResponse is the response payload for the assistant chat endpoint.
type ChatResponse struct {
	Message     string   `json:"message"`
	Suggestions []string `json:"suggestions,omitempty"`
	Commands    []string `json:"commands,omitempty"`
	Context     string   `json:"context,omitempty"`
}

// LogAnalysis summarizes a log snippet.
type LogAnalysis struct {
	Explanation string   `json:"explanation"`
	Severity    string   `json:"severity"`
	Suggestions []string `json:"suggestions"`
}

// CommandHelp describes a shell command.
type CommandHelp struct {
	Command     string   `json:"command"`
	Description string   `json:"description"`
	Examples    []string `json:"examples"`
	Warning     string   `json:"warning,omitempty"`
}

// Service implements a rule-based AI assistant for server migration help.
type Service struct {
	snapshotStore discovery.SnapshotStore
	serverRepo    server.Repo
	pool          *modssh.Pool
	authSvc       transport.AESKeyProvider
	knownHosts    transport.HostKeyStore
}

// NewService creates a new assistant service.
func NewService(
	snapshotStore discovery.SnapshotStore,
	serverRepo server.Repo,
	pool *modssh.Pool,
	authSvc transport.AESKeyProvider,
	knownHosts transport.HostKeyStore,
) *Service {
	return &Service{
		snapshotStore: snapshotStore,
		serverRepo:    serverRepo,
		pool:          pool,
		authSvc:       authSvc,
		knownHosts:    knownHosts,
	}
}

type assistantServerContext struct {
	Server           *server.Server
	Snapshot         *discovery.ServerSnapshot
	PreviousSnapshot *discovery.ServerSnapshot
	ContextSummary   string
}

// Chat processes a chat message and returns a rule-based response.
func (s *Service) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}

	resolvedServerID := req.ServerID
	resolvedServer := (*server.Server)(nil)
	resolvedSnapshot := (*discovery.ServerSnapshot)(nil)
	resolvedPrevious := (*discovery.ServerSnapshot)(nil)
	contextNotes := make([]string, 0, 3)

	if req.ServerID > 0 {
		serverCtx, err := s.loadServerContext(ctx, req.ServerID)
		if err != nil {
			return nil, err
		}
		resolvedServer = serverCtx.Server
		resolvedSnapshot = serverCtx.Snapshot
		resolvedPrevious = serverCtx.PreviousSnapshot
		contextNotes = append(contextNotes, serverCtx.ContextSummary)
	}

	if resolvedServer == nil {
		if matchedID, matchedReason := s.matchServerFromText(ctx, message, req.History); matchedID > 0 {
			resolvedServerID = matchedID
			serverCtx, err := s.loadServerContext(ctx, matchedID)
			if err == nil {
				resolvedServer = serverCtx.Server
				resolvedSnapshot = serverCtx.Snapshot
				resolvedPrevious = serverCtx.PreviousSnapshot
				if matchedReason != "" {
					contextNotes = append(contextNotes, matchedReason)
				}
				contextNotes = append(contextNotes, serverCtx.ContextSummary)
			}
		}
	}

	topic := inferTopic(message, req.History)
	if resolvedServer != nil {
		if topic == "" {
			topic = inferTopic(strings.Join(historyContents(req.History), "\n"), nil)
		}
	}

	if looksLikeCommandHelp(message) {
		command := extractCommandFromHelpRequest(message, req.History)
		if command == "" && topic != "" {
			command = topic
		}
		help, err := s.GetCommandHelp(ctx, command)
		if err != nil {
			return nil, err
		}
		resp := &ChatResponse{
			Message:  fmt.Sprintf("Here’s help for `%s`.\n\n%s", help.Command, help.Description),
			Commands: help.Examples,
			Context:  joinContextNotes(contextNotes),
		}
		if help.Warning != "" {
			resp.Suggestions = append(resp.Suggestions, help.Warning)
		}
		return resp, nil
	}

	if looksLikeLogQuestion(message) || looksLikeLogSnippet(message) {
		analysis, err := s.AnalyzeLog(ctx, resolvedServerID, message)
		if err != nil {
			return nil, err
		}
		return &ChatResponse{
			Message:     fmt.Sprintf("%s\n\n**Severity:** %s", analysis.Explanation, analysis.Severity),
			Suggestions: analysis.Suggestions,
			Context:     joinContextNotes(contextNotes),
		}, nil
	}

	switch {
	case looksLikeMigrationQuestion(message, topic):
		return s.answerMigrationQuestion(ctx, message, resolvedServer, resolvedSnapshot, req.History, contextNotes)
	case looksLikePackageQuestion(message):
		return s.answerPackageQuestion(ctx, message, resolvedServer, resolvedSnapshot, contextNotes)
	case looksLikeServiceStatusQuestion(message):
		return s.answerServiceStatusQuestion(ctx, message, resolvedServer, resolvedSnapshot, contextNotes, req.History)
	case looksLikeFirewallOrDockerQuestion(message):
		return s.answerConfigurationQuestion(ctx, message, resolvedServer, resolvedSnapshot, contextNotes)
	case looksLikeChangeQuestion(message):
		return s.answerChangeQuestion(ctx, resolvedServerID, resolvedServer, resolvedSnapshot, resolvedPrevious, contextNotes)
	default:
		return s.answerGeneralQuestion(ctx, message, resolvedServer, resolvedSnapshot, contextNotes)
	}
}

// GetSuggestions returns proactive suggestions for a server.
func (s *Service) GetSuggestions(ctx context.Context, serverID int) ([]string, error) {
	if serverID <= 0 {
		return []string{"Select a server to get tailored migration and operations suggestions."}, nil
	}

	serverCtx, err := s.loadServerContext(ctx, serverID)
	if err != nil {
		return nil, err
	}

	suggestions := s.suggestionsFromSnapshot(serverCtx.Server, serverCtx.Snapshot)

	if updates, pm, err := s.livePackageUpdateCount(ctx, serverCtx.Server, serverCtx.Snapshot); err == nil && updates > 0 {
		suggestions = append([]string{fmt.Sprintf("Consider updating %d packages on %s using %s.", updates, serverCtx.Server.Name, pm)}, suggestions...)
	}

	if len(suggestions) == 0 {
		suggestions = []string{
			fmt.Sprintf("Run a discovery scan on %s to refresh its snapshot.", serverCtx.Server.Name),
			"Review open ports before a migration.",
			"Check service and container logs for recent errors.",
		}
	}

	return dedupeStrings(suggestions), nil
}

// AnalyzeLog explains a log entry and suggests next steps.
func (s *Service) AnalyzeLog(ctx context.Context, serverID int, logContent string) (*LogAnalysis, error) {
	_ = ctx
	text := strings.ToLower(strings.TrimSpace(logContent))
	if text == "" {
		return nil, fmt.Errorf("log content is required")
	}

	severity := "low"
	explanation := "This log entry looks informational or non-critical."
	suggestions := []string{"Check the surrounding log lines for context.", "Correlate the timestamp with recent deployments or configuration changes."}

	switch {
	case containsAny(text, "out of memory", "oom", "killed process", "kernel panic", "segmentation fault", "panic:"):
		severity = "critical"
		explanation = "The log suggests a severe failure such as an OOM kill, panic, or segmentation fault."
		suggestions = []string{"Check memory pressure with `free -h` or your monitoring tool.", "Inspect kernel and application logs for the crash cause.", "Restart the affected service after identifying the root cause."}
	case containsAny(text, "permission denied", "access denied", "authentication failed"):
		severity = "high"
		explanation = "The log points to an authentication or permission problem."
		suggestions = []string{"Verify file ownership and permissions.", "Check credentials, SSH keys, or service account tokens.", "Confirm the process has access to the required paths and secrets."}
	case containsAny(text, "connection refused", "no route to host", "timed out", "timeout"):
		severity = "medium"
		explanation = "The log indicates a connectivity problem between services or hosts."
		suggestions = []string{"Confirm the target host is reachable and the service is listening.", "Check firewall rules and security groups.", "Verify DNS resolution and network routing."}
	case containsAny(text, "failed to start", "unable to start", "bind: address already in use"):
		severity = "high"
		explanation = "The service failed to start, often due to a config error or a port conflict."
		suggestions = []string{"Inspect the service status with `systemctl status`.", "Check for duplicate listeners on the same port.", "Validate the configuration before restarting."}
	case containsAny(text, "warning", "deprecated", "retrying", "reconnect"):
		severity = "medium"
		explanation = "The log contains a warning or a transient condition that should be monitored."
		suggestions = []string{"Watch whether the warning repeats.", "Investigate dependency health and network stability.", "Plan a follow-up if the warning coincides with latency spikes."}
	}

	if serverID > 0 {
		if serverCtx, err := s.loadServerContext(ctx, serverID); err == nil {
			suggestions = append(suggestions, fmt.Sprintf("Review %s for related service and snapshot data.", serverCtx.Server.Name))
		}
	}

	return &LogAnalysis{
		Explanation: explanation,
		Severity:    severity,
		Suggestions: dedupeStrings(suggestions),
	}, nil
}

// GetCommandHelp provides help for a shell command.
func (s *Service) GetCommandHelp(ctx context.Context, command string) (*CommandHelp, error) {
	_ = ctx
	normalized := strings.TrimSpace(command)
	if normalized == "" {
		return nil, fmt.Errorf("command is required")
	}

	key := commandHelpKey(normalized)
	helper, ok := commandHelpRegistry[key]
	if !ok {
		return &CommandHelp{
			Command:     normalized,
			Description: "I can help explain common server, Docker, package, service, and firewall commands.",
			Examples: []string{
				"systemctl status nginx",
				"docker ps -a",
				"journalctl -u nginx -xe --no-pager",
			},
		}, nil
	}

	copy := helper
	copy.Command = normalized
	return &copy, nil
}

func (s *Service) answerMigrationQuestion(ctx context.Context, message string, srv *server.Server, snap *discovery.ServerSnapshot, history []ChatMessage, contextNotes []string) (*ChatResponse, error) {
	_ = ctx
	serviceName := extractTargetName(message, history)
	if serviceName == "" {
		serviceName = inferMigrationTarget(srv, snap, history)
	}
	if serviceName == "" {
		serviceName = "service"
	}

	lower := strings.ToLower(serviceName)
	suggestions := []string{
		"Inventory the current configuration and data paths before moving anything.",
		"Plan a validation step after the target is online.",
		"Test the migrated workload in a staging environment first when possible.",
	}
	commands := []string{
		"systemctl status " + shared.ShellQuote(serviceName),
		"journalctl -u " + shared.ShellQuote(serviceName) + " -xe --no-pager",
	}

	messageParts := []string{fmt.Sprintf("To migrate **%s**, start with a quick inventory, copy the config/data, and validate the service on the target before switching traffic.", serviceName)}

	switch {
	case strings.Contains(lower, "nginx") || (snap != nil && snap.Nginx != nil):
		messageParts = append(messageParts,
			"For Nginx, back up `/etc/nginx`, copy vhosts and certificates, run `nginx -t`, then reload on the target.",
		)
		commands = append(commands,
			"sudo nginx -t",
			"sudo rsync -a --delete /etc/nginx/ target:/etc/nginx/",
		)
	case strings.Contains(lower, "docker") || (snap != nil && snap.Docker != nil):
		messageParts = append(messageParts,
			"For Docker workloads, export the compose file, environment, and volumes, then bring the stack up on the target with the same image tags.",
		)
		if snap != nil && snap.Docker != nil && len(snap.Docker.ComposeProjects) > 0 {
			proj := snap.Docker.ComposeProjects[0]
			commands = append(commands,
				"cd "+shared.ShellQuote(projectPathHint(proj.ConfigFiles))+" && docker compose up -d",
				"docker ps",
			)
		}
	case strings.Contains(lower, "postgres") || strings.Contains(lower, "mysql") || strings.Contains(lower, "maria") || strings.Contains(lower, "redis"):
		messageParts = append(messageParts,
			"For databases, take a consistent backup, restore it on the target, and verify application connectivity before cutover.",
		)
		commands = append(commands,
			"pg_dumpall > backup.sql",
			"systemctl status "+shared.ShellQuote(serviceName),
		)
	default:
		messageParts = append(messageParts,
			"If this is a systemd service, copy its unit file and config, then enable and start it on the target.",
		)
		commands = append(commands,
			"sudo systemctl enable "+shared.ShellQuote(serviceName),
			"sudo systemctl start "+shared.ShellQuote(serviceName),
		)
	}

	return &ChatResponse{
		Message:     strings.Join(messageParts, "\n\n"),
		Suggestions: suggestions,
		Commands:    dedupeStrings(commands),
		Context:     joinContextNotes(contextNotes),
	}, nil
}

func (s *Service) answerPackageQuestion(ctx context.Context, message string, srv *server.Server, snap *discovery.ServerSnapshot, contextNotes []string) (*ChatResponse, error) {
	_ = message
	packages, pm, err := s.listPackages(ctx, srv, snap)
	if err != nil {
		if srv == nil {
			return &ChatResponse{
				Message:  "I can list packages once a server is selected.",
				Context:  joinContextNotes(contextNotes),
				Commands: []string{"dpkg -l | awk 'NR>5 {print $2}'", "rpm -qa --qf '%{NAME}\n'"},
			}, nil
		}
		return &ChatResponse{
			Message:  fmt.Sprintf("I couldn't read the package list from **%s** right now, but these commands should help.", srv.Name),
			Context:  joinContextNotes(contextNotes),
			Commands: []string{packageListCommandForSnapshot(snap), "systemctl status " + shared.ShellQuote("package-manager")},
		}, nil
	}

	limit := 20
	if len(packages) < limit {
		limit = len(packages)
	}

	var builder strings.Builder
	if srv != nil {
		fmt.Fprintf(&builder, "Here are the first %d installed packages on **%s** (%s).", limit, srv.Name, pm)
	} else {
		fmt.Fprintf(&builder, "Here are the first %d installed packages (%s).", limit, pm)
	}
	if len(packages) > 0 {
		builder.WriteString("\n\n")
		builder.WriteString("- ")
		builder.WriteString(strings.Join(packages[:limit], "\n- "))
	}
	if len(packages) > limit {
		fmt.Fprintf(&builder, "\n\nThere are %d more packages not shown here.", len(packages)-limit)
	}

	suggestions := []string{"Compare installed packages before and after a migration.", "Check for security updates before cutover.", "Document custom packages separately from base OS packages."}
	if srv != nil {
		suggestions = append([]string{fmt.Sprintf("Run an update check on %s to see whether any packages need attention.", srv.Name)}, suggestions...)
	}

	commands := []string{packageListCommandForPackageManager(pm)}
	if srv != nil {
		commands = append(commands, packageUpdateCommandForPackageManager(pm))
	}

	return &ChatResponse{
		Message:     builder.String(),
		Suggestions: dedupeStrings(suggestions),
		Commands:    dedupeStrings(filterEmpty(commands)),
		Context:     joinContextNotes(contextNotes),
	}, nil
}

func (s *Service) answerServiceStatusQuestion(ctx context.Context, message string, srv *server.Server, snap *discovery.ServerSnapshot, contextNotes []string, history []ChatMessage) (*ChatResponse, error) {
	serviceName := extractTargetName(message, history)
	if serviceName == "" {
		serviceName = inferServiceName(snap, message, history)
	}
	if serviceName == "" {
		serviceName = "service"
	}

	status, source := s.serviceStatus(ctx, srv, snap, serviceName)
	var messageText string
	var commands []string
	var suggestions []string

	switch status {
	case "active":
		messageText = fmt.Sprintf("**%s** is running on %s.", serviceName, serverLabel(srv))
		suggestions = []string{"Review logs if you need to investigate a recent restart.", "Use `systemctl status` to confirm the current state."}
	case "inactive", "dead", "failed":
		messageText = fmt.Sprintf("**%s** is not running on %s right now (%s).", serviceName, serverLabel(srv), source)
		suggestions = []string{
			"Check the latest logs for the failing unit.",
			"Verify the config file before restarting.",
			"If it depends on another service, make sure the dependency is healthy first.",
		}
		commands = []string{
			"systemctl status " + shared.ShellQuote(serviceName),
			"journalctl -u " + shared.ShellQuote(serviceName) + " -xe --no-pager",
			"sudo systemctl restart " + shared.ShellQuote(serviceName),
		}
	default:
		messageText = fmt.Sprintf("I couldn't confirm the state of **%s** on %s, so the safest next step is to check the service status and logs.", serviceName, serverLabel(srv))
		suggestions = []string{"Check whether the service exists on the server.", "Look for dependency failures or port conflicts.", "Confirm that the configuration is valid before restarting."}
		commands = []string{
			"systemctl status " + shared.ShellQuote(serviceName),
			"sudo systemctl restart " + shared.ShellQuote(serviceName),
		}
	}

	if srv == nil {
		messageText = fmt.Sprintf("I don't have a selected server, but you can check **%s** with the commands below.", serviceName)
	}

	return &ChatResponse{
		Message:     messageText,
		Suggestions: dedupeStrings(suggestions),
		Commands:    dedupeStrings(commands),
		Context:     joinContextNotes(contextNotes),
	}, nil
}

func (s *Service) answerConfigurationQuestion(ctx context.Context, message string, srv *server.Server, snap *discovery.ServerSnapshot, contextNotes []string) (*ChatResponse, error) {
	_ = ctx
	lower := strings.ToLower(message)
	if strings.Contains(lower, "docker") {
		commands := []string{"sudo systemctl enable --now docker", "docker ps", "docker compose up -d"}
		suggestions := []string{"Back up compose files and environment variables before changes.", "Check container logs after the stack comes back up.", "Use pinned image tags instead of floating latest tags."}
		if snap != nil && snap.Docker != nil && len(snap.Docker.ComposeProjects) > 0 {
			proj := snap.Docker.ComposeProjects[0]
			commands = append(commands, "cd "+shared.ShellQuote(projectPathHint(proj.ConfigFiles))+" && docker compose up -d")
			suggestions = append([]string{fmt.Sprintf("I found a compose project on %s that can be restarted with Docker Compose.", serverLabel(srv))}, suggestions...)
		}
		return &ChatResponse{
			Message:     "For Docker, the usual path is to validate the compose file, start the daemon, and then bring the stack up in the background.",
			Suggestions: suggestions,
			Commands:    dedupeStrings(commands),
			Context:     joinContextNotes(contextNotes),
		}, nil
	}

	if containsAny(lower, "firewall", "ufw", "firewalld", "firewall-cmd") {
		suggestions := []string{"Open only the ports that are required by the service.", "Test connectivity after adding the rule.", "Persist rules before rebooting the server."}
		commands := []string{"sudo ufw allow 80/tcp", "sudo ufw allow 443/tcp", "sudo firewall-cmd --permanent --add-port=80/tcp", "sudo firewall-cmd --reload"}
		if snap != nil && len(snap.NetworkPorts) > 0 {
			ports := make([]string, 0, len(snap.NetworkPorts))
			for _, p := range snap.NetworkPorts {
				ports = append(ports, strconv.Itoa(p.Port)+"/"+p.Protocol)
			}
			suggestions = append([]string{fmt.Sprintf("Open ports detected on %s: %s", serverLabel(srv), strings.Join(ports, ", "))}, suggestions...)
		}
		return &ChatResponse{
			Message:     "Firewall configuration depends on the distro, but the most common options are UFW on Debian/Ubuntu and firewalld on RHEL-like systems.",
			Suggestions: suggestions,
			Commands:    commands,
			Context:     joinContextNotes(contextNotes),
		}, nil
	}

	return &ChatResponse{
		Message: "I can help with Docker, firewall rules, services, packages, and migration planning. Tell me what you want to change and I’ll suggest the exact commands.",
		Suggestions: []string{
			"Ask for a migration plan for a specific service.",
			"Ask what packages are installed on a server.",
			"Paste an error log and I’ll explain it.",
		},
		Commands: []string{"systemctl status", "docker ps", "journalctl -xe --no-pager"},
		Context:  joinContextNotes(contextNotes),
	}, nil
}

func (s *Service) answerChangeQuestion(ctx context.Context, serverID int, srv *server.Server, curr *discovery.ServerSnapshot, prev *discovery.ServerSnapshot, contextNotes []string) (*ChatResponse, error) {
	_ = ctx
	if srv == nil {
		return &ChatResponse{
			Message: "Select a server and I can compare its latest snapshots to show what changed.",
			Suggestions: []string{
				"Run a discovery scan first.",
				"Then ask me again for a snapshot diff.",
			},
			Context: joinContextNotes(contextNotes),
		}, nil
	}
	if curr == nil {
		return &ChatResponse{
			Message:     fmt.Sprintf("I couldn't find a discovery snapshot for **%s** yet.", srv.Name),
			Suggestions: []string{"Run discovery to collect a baseline snapshot.", "Then ask what changed again after the next scan."},
			Context:     joinContextNotes(contextNotes),
		}, nil
	}
	if prev == nil {
		return &ChatResponse{
			Message:     fmt.Sprintf("I found the latest snapshot for **%s**, but I don't have an earlier snapshot to compare against yet.", srv.Name),
			Suggestions: []string{"Run discovery again later to build a comparison history.", "Use the snapshot to review services, Docker, Nginx, and open ports."},
			Context:     joinContextNotes(contextNotes),
		}, nil
	}

	changes, suggestions := diffSnapshots(curr, prev)
	if len(changes) == 0 {
		return &ChatResponse{
			Message:     fmt.Sprintf("I compared the two latest snapshots for **%s** and didn't find any major changes.", srv.Name),
			Suggestions: []string{"Check logs if you expected a change that isn't reflected in the snapshot.", "Run another discovery scan to confirm current state."},
			Context:     joinContextNotes(contextNotes),
		}, nil
	}

	return &ChatResponse{
		Message:     fmt.Sprintf("Here’s what changed on **%s** between the two latest snapshots:\n\n- %s", srv.Name, strings.Join(changes, "\n- ")),
		Suggestions: dedupeStrings(suggestions),
		Context:     joinContextNotes(contextNotes),
	}, nil
}

func (s *Service) answerGeneralQuestion(ctx context.Context, message string, srv *server.Server, snap *discovery.ServerSnapshot, contextNotes []string) (*ChatResponse, error) {
	_ = ctx
	parts := []string{"I can help you plan migrations, explain service failures, review logs, and suggest common shell commands."}
	if srv != nil {
		parts = append(parts, fmt.Sprintf("For **%s** (%s), I can tailor advice using the latest discovery snapshot.", srv.Name, serverSummaryLine(srv, snap)))
	}
	if snap != nil {
		parts = append(parts, snapshotSummaryText(snap))
	}

	return &ChatResponse{
		Message: strings.Join(parts, "\n\n"),
		Suggestions: []string{
			"Ask how to migrate a specific service.",
			"Ask what packages are installed on the selected server.",
			"Paste an error log for analysis.",
		},
		Commands: []string{"systemctl status", "journalctl -xe --no-pager", "docker ps -a"},
		Context:  joinContextNotes(contextNotes),
	}, nil
}

func (s *Service) loadServerContext(ctx context.Context, serverID int) (*assistantServerContext, error) {
	if s.serverRepo == nil {
		return nil, fmt.Errorf("server repository is not configured")
	}
	if serverID <= 0 {
		return nil, fmt.Errorf("invalid server id")
	}

	srv, err := s.serverRepo.GetByID(serverID)
	if err != nil {
		return nil, err
	}

	ctxSummary := serverSummaryLine(srv, nil)
	var snap *discovery.ServerSnapshot
	if s.snapshotStore != nil {
		if latest, err := s.snapshotStore.LoadSnapshot(serverID); err == nil {
			snap = latest
			ctxSummary = serverSummaryLine(srv, latest)
		}
	}

	var prev *discovery.ServerSnapshot
	if s.snapshotStore != nil && snap != nil {
		if previous, err := s.snapshotStore.LoadSnapshotAt(serverID, snap.CapturedAt.Add(-time.Nanosecond)); err == nil {
			prev = previous
		}
	}

	return &assistantServerContext{
		Server:           srv,
		Snapshot:         snap,
		PreviousSnapshot: prev,
		ContextSummary:   ctxSummary,
	}, nil
}

func (s *Service) matchServerFromText(ctx context.Context, message string, history []ChatMessage) (int, string) {
	_ = ctx
	if s.serverRepo == nil {
		return 0, ""
	}
	servers, err := s.serverRepo.List(server.ListFilter{})
	if err != nil {
		return 0, ""
	}

	text := strings.ToLower(strings.Join(append(historyContents(history), message), "\n"))
	var bestID int
	bestScore := 0
	bestReason := ""
	for _, srv := range servers {
		candidates := []string{strings.ToLower(srv.Name), strings.ToLower(srv.Host), strconv.Itoa(srv.ID)}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if strings.Contains(text, candidate) {
				score := len(candidate)
				if score > bestScore {
					bestScore = score
					bestID = srv.ID
					bestReason = fmt.Sprintf("Matched server %q from the conversation context.", srv.Name)
				}
			}
		}
	}
	return bestID, bestReason
}

func (s *Service) hostKeyCallback(serverID int) ssh.HostKeyCallback {
	if s.knownHosts != nil {
		return s.knownHosts.MakeHostKeyCallback(serverID)
	}
	// No store to verify against: fail closed rather than silently accepting any
	// key. These dials carry decrypted credentials, so an insecure fallback would
	// expose them to a man-in-the-middle.
	return modssh.FailClosedHostKeyCallback()
}

func (s *Service) buildSSHConfig(serverID int, seen map[int]bool) (*modssh.ServerConfig, *server.Server, error) {
	if seen[serverID] {
		return nil, nil, fmt.Errorf("circular bastion reference detected for server %d", serverID)
	}
	seen[serverID] = true

	srv, err := s.serverRepo.GetByID(serverID)
	if err != nil {
		return nil, nil, fmt.Errorf("server %d not found: %w", serverID, err)
	}

	password, sshKey, passphrase, err := s.decryptServerCredentials(srv)
	if err != nil {
		return nil, nil, err
	}

	cfg := &modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	if srv.BastionID > 0 {
		bastionSrv, err := s.serverRepo.GetByID(srv.BastionID)
		if err != nil {
			return nil, nil, fmt.Errorf("load bastion server %d: %w", srv.BastionID, err)
		}
		if bastionSrv.BastionID > 0 {
			return nil, nil, fmt.Errorf("nested bastion connections are not supported")
		}

		bastionPassword, bastionSSHKey, bastionPassphrase, err := s.decryptServerCredentials(bastionSrv)
		if err != nil {
			return nil, nil, err
		}
		bastionCfg := &modssh.BastionConfig{
			Host:            bastionSrv.Host,
			Port:            bastionSrv.Port,
			Username:        bastionSrv.Username,
			Password:        bastionPassword,
			Passphrase:      bastionPassphrase,
			HostKeyCallback: s.hostKeyCallback(bastionSrv.ID),
		}
		if bastionSSHKey != "" {
			bastionCfg.PrivateKey = []byte(bastionSSHKey)
		}
		cfg.Bastion = bastionCfg
	}

	return cfg, srv, nil
}

func (s *Service) decryptServerCredentials(srv *server.Server) (password, sshKey, passphrase string, err error) {
	if srv == nil {
		return "", "", "", fmt.Errorf("server is nil")
	}
	if s.authSvc == nil {
		return "", "", "", fmt.Errorf("app is locked — cannot decrypt credentials")
	}
	key := s.authSvc.GetAESKey()
	if key == nil {
		return "", "", "", fmt.Errorf("app is locked — cannot decrypt credentials")
	}
	password, err = decryptCredential(key, srv.Password)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt password: %w", err)
	}
	sshKey, err = decryptCredential(key, srv.SSHKey)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt ssh key: %w", err)
	}
	passphrase, err = decryptCredential(key, srv.Passphrase)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt passphrase: %w", err)
	}
	return password, sshKey, passphrase, nil
}

func (s *Service) getSSHClient(ctx context.Context, serverID int) (transport.SSHExecuter, *server.Server, error) {
	if s.pool == nil {
		return nil, nil, fmt.Errorf("ssh pool is not configured")
	}

	cfg, srv, err := s.buildSSHConfig(serverID, map[int]bool{})
	if err != nil {
		return nil, nil, err
	}

	client, err := s.pool.GetContext(ctx, serverID, *cfg, s.hostKeyCallback(serverID))
	if err != nil {
		return nil, nil, err
	}
	return client, srv, nil
}

func (s *Service) livePackageUpdateCount(ctx context.Context, srv *server.Server, snap *discovery.ServerSnapshot) (int, string, error) {
	if srv == nil {
		return 0, "", fmt.Errorf("server is nil")
	}
	client, _, err := s.getSSHClient(ctx, srv.ID)
	if err != nil {
		return 0, "", err
	}

	distro, err := migration.DetectDistro(ctx, client)
	if err != nil {
		return 0, "", err
	}
	pm := distro.PackageManager
	if pm == "" {
		pm = packageManagerFromSnapshot(snap)
	}
	cmd := packageUpdateCommandForPackageManager(pm)
	if cmd == "" {
		return 0, pm, fmt.Errorf("unsupported package manager: %s", pm)
	}

	stdout, stderr, exitCode, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return 0, pm, err
	}
	if exitCode != 0 && exitCode != 100 {
		return 0, pm, fmt.Errorf("package update check failed: %s", combineOutput(stdout, stderr))
	}

	count, parseErr := strconv.Atoi(strings.TrimSpace(stdout))
	if parseErr != nil {
		return 0, pm, parseErr
	}
	return count, pm, nil
}

func (s *Service) listPackages(ctx context.Context, srv *server.Server, snap *discovery.ServerSnapshot) ([]string, string, error) {
	if srv == nil {
		return nil, "", fmt.Errorf("server is nil")
	}
	client, _, err := s.getSSHClient(ctx, srv.ID)
	if err != nil {
		return nil, "", err
	}

	distro, err := migration.DetectDistro(ctx, client)
	if err != nil {
		return nil, "", err
	}
	adapter, err := migration.GetAdapter(distro)
	if err != nil {
		return nil, "", err
	}
	stdout, stderr, exitCode, err := client.ExecContext(ctx, adapter.ListPackages())
	if err != nil {
		return nil, "", err
	}
	if exitCode != 0 {
		return nil, adapter.PackageManager(), fmt.Errorf("package listing failed: %s", combineOutput(stdout, stderr))
	}
	packages := parsePackageList(stdout, adapter.PackageManager())
	if len(packages) == 0 && snap != nil {
		return []string{}, adapter.PackageManager(), nil
	}
	return packages, adapter.PackageManager(), nil
}

func (s *Service) serviceStatus(ctx context.Context, srv *server.Server, snap *discovery.ServerSnapshot, serviceName string) (string, string) {
	if snap != nil {
		for _, svc := range snap.Services {
			if strings.EqualFold(strings.TrimSuffix(svc.Name, ".service"), strings.TrimSpace(serviceName)) || strings.EqualFold(svc.Name, serviceName) {
				return strings.ToLower(svc.ActiveState), "snapshot"
			}
		}
	}
	if srv == nil {
		return "unknown", "no server"
	}
	client, _, err := s.getSSHClient(ctx, srv.ID)
	if err != nil {
		return "unknown", err.Error()
	}
	stdout, _, _, err := client.ExecContext(ctx, "systemctl is-active "+shared.ShellQuote(serviceName))
	if err != nil {
		return "unknown", err.Error()
	}
	state := strings.TrimSpace(stdout)
	if state == "" {
		return "unknown", "live"
	}
	return state, "live"
}

func (s *Service) suggestionsFromSnapshot(srv *server.Server, snap *discovery.ServerSnapshot) []string {
	if snap == nil {
		if srv != nil {
			return []string{fmt.Sprintf("Run a discovery scan on %s to collect services, Docker, Nginx, disk usage, and ports.", srv.Name)}
		}
		return nil
	}

	suggestions := make([]string, 0, 8)

	if snap.Docker != nil {
		if len(snap.Docker.ComposeProjects) > 0 {
			for _, proj := range snap.Docker.ComposeProjects {
				suggestions = append(suggestions, fmt.Sprintf("Review Docker Compose project %s before migrating it.", proj.Name))
			}
		} else if len(snap.Docker.Containers) > 0 {
			suggestions = append(suggestions, fmt.Sprintf("Inspect %d Docker container(s) before cutover.", len(snap.Docker.Containers)))
		}
	}

	if snap.Nginx != nil {
		expiring := 0
		for _, cert := range snap.Nginx.SSLCerts {
			if cert.DaysRemaining > 0 && cert.DaysRemaining <= 30 {
				expiring++
			}
		}
		if expiring > 0 {
			suggestions = append(suggestions, fmt.Sprintf("Renew %d SSL certificate(s) that expire within 30 days.", expiring))
		}
		suggestions = append(suggestions, "Validate Nginx config with `nginx -t` before reloading.")
	}

	for _, db := range snap.Databases {
		if db.Running {
			suggestions = append(suggestions, fmt.Sprintf("Back up the %s database before migrating %s.", db.Type, db.Type))
		}
	}

	for _, part := range snap.DiskUsage {
		if part.UsePercent > 90 {
			suggestions = append(suggestions, fmt.Sprintf("Free space on %s is critical at %.0f%% usage.", part.MountPoint, part.UsePercent))
		} else if part.UsePercent > 75 {
			suggestions = append(suggestions, fmt.Sprintf("Monitor disk usage on %s (%.0f%% used).", part.MountPoint, part.UsePercent))
		}
	}

	for _, port := range snap.NetworkPorts {
		if port.Port == 80 || port.Port == 443 || port.Port == 22 {
			continue
		}
		suggestions = append(suggestions, fmt.Sprintf("Document the purpose of listening port %d/%s before the migration.", port.Port, port.Protocol))
		break
	}

	if len(suggestions) == 0 {
		suggestions = append(suggestions, "Run a discovery scan to populate more context for migration planning.")
	}

	return dedupeStrings(suggestions)
}

func (s *Service) loadSnapshotPair(serverID int) (*discovery.ServerSnapshot, *discovery.ServerSnapshot, error) {
	if s.snapshotStore == nil {
		return nil, nil, nil
	}
	curr, err := s.snapshotStore.LoadSnapshot(serverID)
	if err != nil {
		return nil, nil, nil
	}
	prev, err := s.snapshotStore.LoadSnapshotAt(serverID, curr.CapturedAt.Add(-time.Nanosecond))
	if err != nil {
		return curr, nil, nil
	}
	return curr, prev, nil
}

func inferTopic(message string, history []ChatMessage) string {
	text := strings.ToLower(strings.Join(append(historyContents(history), message), "\n"))
	switch {
	case containsAny(text, "migrate", "migration", "move", "cutover", "transfer"):
		return "migration"
	case containsAny(text, "docker", "compose", "container", "image"):
		return "docker"
	case containsAny(text, "firewall", "ufw", "firewalld", "port"):
		return "firewall"
	case containsAny(text, "package", "apt", "dnf", "yum", "apk", "pacman", "zypper"):
		return "package"
	case containsAny(text, "journalctl", "log", "error", "fail", "panic", "exception"):
		return "log"
	case containsAny(text, "nginx", "apache", "service", "systemctl"):
		return "service"
	default:
		return ""
	}
}

func looksLikeCommandHelp(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	return strings.HasPrefix(lower, "help with ") || strings.HasPrefix(lower, "how do i use ") || strings.Contains(lower, " command ")
}

func looksLikeLogQuestion(message string) bool {
	lower := strings.ToLower(message)
	return containsAny(lower, "log", "error", "failed", "panic", "traceback") && containsAny(lower, "why", "what", "how")
}

func looksLikeLogSnippet(message string) bool {
	lower := strings.ToLower(message)
	return containsAny(lower, "error", "panic", "exception", "failed", "connection refused", "permission denied") && len(strings.Fields(message)) > 6
}

func looksLikeMigrationQuestion(message, topic string) bool {
	lower := strings.ToLower(message)
	if containsAny(lower, "how do i migrate", "migrate", "migration") {
		return true
	}
	return topic == "migration"
}

func looksLikePackageQuestion(message string) bool {
	lower := strings.ToLower(message)
	return containsAny(lower, "what packages", "list packages", "packages are installed", "installed packages")
}

func looksLikeServiceStatusQuestion(message string) bool {
	lower := strings.ToLower(message)
	return containsAny(lower, "why is", "not running", "service down", "service stopped", "service failed")
}

func looksLikeFirewallOrDockerQuestion(message string) bool {
	lower := strings.ToLower(message)
	return containsAny(lower, "docker", "firewall", "ufw", "firewalld") && containsAny(lower, "configure", "set up", "setup", "how do i")
}

func looksLikeChangeQuestion(message string) bool {
	lower := strings.ToLower(message)
	return containsAny(lower, "what changed", "compare snapshots", "diff the latest", "recent changes")
}

func extractTargetName(message string, history []ChatMessage) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)migrate\s+(?:the\s+)?([a-z0-9._:-]+(?:\s+[a-z0-9._:-]+)?)`),
		regexp.MustCompile(`(?i)why is\s+([a-z0-9._:-]+(?:\s+[a-z0-9._:-]+)?)\s+not running`),
		regexp.MustCompile(`(?i)help with\s+(.+)`),
	}
	for _, pattern := range patterns {
		if m := pattern.FindStringSubmatch(message); len(m) > 1 {
			return strings.Trim(strings.TrimSpace(m[1]), "'\"`.,!?")
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if strings.TrimSpace(history[i].Content) == "" {
			continue
		}
		if m := patterns[0].FindStringSubmatch(history[i].Content); len(m) > 1 {
			return strings.Trim(strings.TrimSpace(m[1]), "'\"`.,!?")
		}
	}
	return ""
}

func extractCommandFromHelpRequest(message string, history []ChatMessage) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)help with\s+(.+)`),
		regexp.MustCompile(`(?i)how do i use\s+(.+)`),
	}
	for _, pattern := range patterns {
		if m := pattern.FindStringSubmatch(message); len(m) > 1 {
			return strings.Trim(strings.TrimSpace(m[1]), "'\"`.,!?")
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if m := patterns[0].FindStringSubmatch(history[i].Content); len(m) > 1 {
			return strings.Trim(strings.TrimSpace(m[1]), "'\"`.,!?")
		}
	}
	return ""
}

func inferMigrationTarget(srv *server.Server, snap *discovery.ServerSnapshot, history []ChatMessage) string {
	text := strings.ToLower(strings.Join(append(historyContents(history), serverSummaryLine(srv, snap)), "\n"))
	switch {
	case strings.Contains(text, "nginx"):
		return "nginx"
	case strings.Contains(text, "postgres"):
		return "postgresql"
	case strings.Contains(text, "mysql"):
		return "mysql"
	case strings.Contains(text, "redis"):
		return "redis"
	case strings.Contains(text, "docker"):
		return "docker"
	default:
		return ""
	}
}

func inferServiceName(snap *discovery.ServerSnapshot, message string, history []ChatMessage) string {
	text := strings.ToLower(strings.Join(append(historyContents(history), message), "\n"))
	if snap != nil {
		for _, svc := range snap.Services {
			name := strings.ToLower(strings.TrimSuffix(svc.Name, ".service"))
			if strings.Contains(text, name) {
				return strings.TrimSuffix(svc.Name, ".service")
			}
		}
	}
	for _, candidate := range []string{"nginx", "docker", "postgresql", "postgres", "mysql", "redis", "ssh", "cron", "php-fpm", "apache2"} {
		if strings.Contains(text, candidate) {
			return candidate
		}
	}
	return ""
}

func historyContents(history []ChatMessage) []string {
	contents := make([]string, 0, len(history))
	for _, msg := range history {
		if strings.TrimSpace(msg.Content) != "" {
			contents = append(contents, msg.Content)
		}
	}
	return contents
}

func joinContextNotes(notes []string) string {
	filtered := make([]string, 0, len(notes))
	for _, note := range notes {
		if strings.TrimSpace(note) != "" {
			filtered = append(filtered, note)
		}
	}
	return strings.Join(dedupeStrings(filtered), "\n")
}

func snapshotSummaryText(snap *discovery.ServerSnapshot) string {
	if snap == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("Snapshot captured at %s.", snap.CapturedAt.Format(time.RFC3339))}
	if snap.OS.Distro != "" {
		parts = append(parts, fmt.Sprintf("OS: %s (%s).", snap.OS.Distro, snap.OS.Kernel))
	}
	if snap.Docker != nil {
		parts = append(parts, fmt.Sprintf("Docker: %d container(s), %d image(s), %d compose project(s).", len(snap.Docker.Containers), len(snap.Docker.Images), len(snap.Docker.ComposeProjects)))
	}
	if snap.Nginx != nil {
		parts = append(parts, fmt.Sprintf("Nginx: %d vhost(s), %d SSL cert(s).", len(snap.Nginx.VHosts), len(snap.Nginx.SSLCerts)))
	}
	if len(snap.Services) > 0 {
		parts = append(parts, fmt.Sprintf("Active services captured: %d.", len(snap.Services)))
	}
	if len(snap.NetworkPorts) > 0 {
		parts = append(parts, fmt.Sprintf("Open ports captured: %d.", len(snap.NetworkPorts)))
	}
	return strings.Join(parts, " ")
}

func serverSummaryLine(srv *server.Server, snap *discovery.ServerSnapshot) string {
	if srv == nil {
		return ""
	}
	parts := []string{srv.Name, srv.Host}
	if snap != nil {
		if snap.OS.Distro != "" {
			parts = append(parts, snap.OS.Distro)
		}
		if snap.OS.Hostname != "" {
			parts = append(parts, snap.OS.Hostname)
		}
	}
	return strings.Join(parts, " · ")
}

func serverLabel(srv *server.Server) string {
	if srv == nil {
		return "the selected server"
	}
	return srv.Name
}

func combineOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout != "" && stderr != "" {
		return stdout + ": " + stderr
	}
	if stdout != "" {
		return stdout
	}
	if stderr != "" {
		return stderr
	}
	return "unknown error"
}

func packageManagerFromSnapshot(snap *discovery.ServerSnapshot) string {
	if snap == nil {
		return ""
	}
	lower := strings.ToLower(snap.OS.Distro)
	switch {
	case strings.Contains(lower, "ubuntu"), strings.Contains(lower, "debian"), strings.Contains(lower, "mint"):
		return "apt"
	case strings.Contains(lower, "rhel"), strings.Contains(lower, "centos"), strings.Contains(lower, "rocky"), strings.Contains(lower, "alma"), strings.Contains(lower, "fedora"):
		return "dnf"
	case strings.Contains(lower, "arch"):
		return "pacman"
	case strings.Contains(lower, "alpine"):
		return "apk"
	case strings.Contains(lower, "suse"):
		return "zypper"
	default:
		return ""
	}
}

func packageListCommandForSnapshot(snap *discovery.ServerSnapshot) string {
	return packageListCommandForPackageManager(packageManagerFromSnapshot(snap))
}

func packageListCommandForPackageManager(pm string) string {
	switch pm {
	case "apt":
		return "dpkg -l | awk 'NR>5 {print $2}'"
	case "dnf", "yum":
		return "rpm -qa --qf '%{NAME}\n'"
	case "pacman":
		return "pacman -Q --qf '%n\n'"
	case "apk":
		return "apk info -v | awk '{print $1}'"
	case "zypper":
		return "zypper se --installed-only | awk 'NR>2 {print $3}'"
	default:
		return ""
	}
}

func packageUpdateCommandForPackageManager(pm string) string {
	switch pm {
	case "apt":
		return "apt list --upgradable 2>/dev/null | tail -n +2 | grep -vc '^$'"
	case "dnf", "yum":
		return "dnf check-update 2>/dev/null | awk 'NF && $1 !~ /^Last metadata/ {count++} END {print count+0}'"
	case "pacman":
		return "checkupdates 2>/dev/null | wc -l"
	case "apk":
		return "apk version -l '<' 2>/dev/null | wc -l"
	case "zypper":
		return "zypper list-updates 2>/dev/null | awk 'NR>2 && NF {count++} END {print count+0}'"
	default:
		return ""
	}
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

func parsePackageName(line, pm string) string {
	switch pm {
	case "apt":
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "ii" || fields[0] == "hi") {
			return fields[1]
		}
		return ""
	case "dnf", "yum":
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
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			return fields[0]
		}
		return ""
	case "apk":
		idx := strings.LastIndex(line, "-")
		if idx > 0 {
			return line[:idx]
		}
		return line
	case "zypper":
		fields := strings.Split(line, "|")
		if len(fields) >= 2 {
			return strings.TrimSpace(fields[1])
		}
		return ""
	default:
		return strings.TrimSpace(line)
	}
}

func diffSnapshots(curr, prev *discovery.ServerSnapshot) ([]string, []string) {
	changes := make([]string, 0, 8)
	suggestions := make([]string, 0, 4)

	if curr.OS.Distro != prev.OS.Distro && curr.OS.Distro != "" && prev.OS.Distro != "" {
		changes = append(changes, fmt.Sprintf("OS changed from %s to %s", prev.OS.Distro, curr.OS.Distro))
		suggestions = append(suggestions, "Validate package compatibility after the OS change.")
	}
	if curr.OS.Kernel != prev.OS.Kernel && curr.OS.Kernel != "" && prev.OS.Kernel != "" {
		changes = append(changes, fmt.Sprintf("Kernel changed from %s to %s", prev.OS.Kernel, curr.OS.Kernel))
	}

	currServices := setFromServices(curr.Services)
	prevServices := setFromServices(prev.Services)
	addedServices := diffSet(currServices, prevServices)
	removedServices := diffSet(prevServices, currServices)
	if len(addedServices) > 0 {
		changes = append(changes, fmt.Sprintf("New active service(s): %s", strings.Join(addedServices, ", ")))
	}
	if len(removedServices) > 0 {
		changes = append(changes, fmt.Sprintf("Missing service(s) compared with the previous snapshot: %s", strings.Join(removedServices, ", ")))
		suggestions = append(suggestions, "Inspect the missing services with `systemctl status` and the relevant logs.")
	}

	currContainers := setFromContainers(curr.Docker)
	prevContainers := setFromContainers(prev.Docker)
	addedContainers := diffSet(currContainers, prevContainers)
	removedContainers := diffSet(prevContainers, currContainers)
	if len(addedContainers) > 0 {
		changes = append(changes, fmt.Sprintf("New Docker container(s): %s", strings.Join(addedContainers, ", ")))
	}
	if len(removedContainers) > 0 {
		changes = append(changes, fmt.Sprintf("Removed Docker container(s): %s", strings.Join(removedContainers, ", ")))
		suggestions = append(suggestions, "Review the deployment or compose file for the removed container.")
	}

	currPorts := setFromPorts(curr.NetworkPorts)
	prevPorts := setFromPorts(prev.NetworkPorts)
	addedPorts := diffSet(currPorts, prevPorts)
	removedPorts := diffSet(prevPorts, currPorts)
	if len(addedPorts) > 0 {
		changes = append(changes, fmt.Sprintf("New listening port(s): %s", strings.Join(addedPorts, ", ")))
	}
	if len(removedPorts) > 0 {
		changes = append(changes, fmt.Sprintf("No longer listening on: %s", strings.Join(removedPorts, ", ")))
	}

	if len(curr.DiskUsage) > 0 && len(prev.DiskUsage) > 0 {
		currUse := curr.DiskUsage[0].UsePercent
		prevUse := prev.DiskUsage[0].UsePercent
		if currUse != prevUse {
			changes = append(changes, fmt.Sprintf("Root disk usage changed from %.0f%% to %.0f%%", prevUse, currUse))
			if currUse > 90 {
				suggestions = append(suggestions, "Free disk space before migrating more data.")
			}
		}
	}

	if len(changes) == 0 {
		return nil, suggestions
	}
	return changes, suggestions
}

func setFromServices(services []discovery.SystemService) map[string]struct{} {
	out := make(map[string]struct{}, len(services))
	for _, svc := range services {
		name := strings.TrimSuffix(strings.ToLower(svc.Name), ".service")
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func setFromContainers(docker *discovery.DockerInfo) map[string]struct{} {
	if docker == nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(docker.Containers))
	for _, c := range docker.Containers {
		if n := strings.ToLower(strings.TrimSpace(c.Name)); n != "" {
			out[n] = struct{}{}
		}
	}
	return out
}

func setFromPorts(ports []discovery.OpenPort) map[string]struct{} {
	out := make(map[string]struct{}, len(ports))
	for _, p := range ports {
		out[fmt.Sprintf("%d/%s", p.Port, p.Protocol)] = struct{}{}
	}
	return out
}

func diffSet(a, b map[string]struct{}) []string {
	var out []string
	for key := range a {
		if _, ok := b[key]; !ok {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func projectPathHint(configFiles string) string {
	configFiles = strings.TrimSpace(configFiles)
	if configFiles == "" {
		return "."
	}
	parts := strings.Split(configFiles, ",")
	first := strings.TrimSpace(parts[0])
	if first == "" {
		return "."
	}
	if strings.Contains(first, "/") {
		return strings.TrimSpace(first[:strings.LastIndex(first, "/")])
	}
	return "."
}

func commandHelpKey(command string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(command)))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) >= 2 && fields[0] == "docker" && fields[1] == "compose" {
		return "docker compose"
	}
	if len(fields) >= 2 && fields[0] == "firewall-cmd" && strings.HasPrefix(fields[1], "--") {
		return "firewall-cmd"
	}
	return fields[0]
}

var commandHelpRegistry = map[string]CommandHelp{
	"systemctl": {
		Command:     "systemctl",
		Description: "Manage systemd services on Linux.",
		Examples: []string{
			"systemctl status nginx",
			"sudo systemctl restart nginx",
			"sudo systemctl enable --now nginx",
		},
		Warning: "Restarting services can interrupt traffic; validate the config first.",
	},
	"journalctl": {
		Command:     "journalctl",
		Description: "Inspect systemd logs for a unit or the entire system.",
		Examples: []string{
			"journalctl -u nginx -xe --no-pager",
			"journalctl -f",
		},
	},
	"docker": {
		Command:     "docker",
		Description: "Manage Docker containers, images, networks, and volumes.",
		Examples: []string{
			"docker ps -a",
			"docker logs --tail 100 nginx",
			"docker inspect nginx",
		},
		Warning: "Be careful with destructive commands such as `docker rm -f` and `docker system prune`.",
	},
	"docker compose": {
		Command:     "docker compose",
		Description: "Start and manage multi-container applications defined by a compose file.",
		Examples: []string{
			"docker compose up -d",
			"docker compose logs -f",
			"docker compose down",
		},
	},
	"ufw": {
		Command:     "ufw",
		Description: "Simple firewall management for Ubuntu and Debian systems.",
		Examples: []string{
			"sudo ufw allow 80/tcp",
			"sudo ufw status verbose",
		},
	},
	"firewall-cmd": {
		Command:     "firewall-cmd",
		Description: "Manage firewalld rules on RHEL-like systems.",
		Examples: []string{
			"sudo firewall-cmd --permanent --add-port=80/tcp",
			"sudo firewall-cmd --reload",
		},
	},
	"apt": {
		Command:     "apt",
		Description: "Debian and Ubuntu package management.",
		Examples: []string{
			"sudo apt update",
			"sudo apt upgrade",
			"apt list --upgradable",
		},
	},
	"dnf": {
		Command:     "dnf",
		Description: "Package management for modern RHEL-based systems.",
		Examples: []string{
			"sudo dnf update",
			"dnf list updates",
		},
	},
	"yum": {
		Command:     "yum",
		Description: "Package management for older RHEL-based systems.",
		Examples: []string{
			"sudo yum update",
			"yum check-update",
		},
	},
	"rsync": {
		Command:     "rsync",
		Description: "Copy files efficiently while preserving permissions, ownership, and timestamps.",
		Examples: []string{
			"rsync -a --delete /etc/nginx/ target:/etc/nginx/",
			"rsync -av /var/www/ target:/var/www/",
		},
	},
	"ssh": {
		Command:     "ssh",
		Description: "Open a secure shell connection to a remote host.",
		Examples: []string{
			"ssh user@example.com",
			"ssh -p 2222 user@example.com",
		},
	},
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func filterEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func decryptCredential(key []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}
