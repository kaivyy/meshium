package firewall

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"meshium/internal/mod/discovery"
	modserver "meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// FirewallStatus describes the current firewall state.
type FirewallStatus struct {
	Type    string `json:"type"` // "ufw", "iptables", "firewalld", "none"
	Active  bool   `json:"active"`
	Default string `json:"default"` // default policy
}

// FirewallRule describes a single firewall rule.
type FirewallRule struct {
	ID        string `json:"id"`
	Action    string `json:"action"`    // ALLOW, DENY, REJECT, DROP
	Protocol  string `json:"protocol"`  // tcp, udp, both
	Port      string `json:"port"`      // port number or range
	Source    string `json:"source"`    // source IP/mask, "any" if empty
	Direction string `json:"direction"` // IN, OUT, FORWARD
}

// FirewallRules contains the firewall status and parsed rules.
type FirewallRules struct {
	Status FirewallStatus `json:"status"`
	Rules  []FirewallRule `json:"rules"`
	IPv6   []FirewallRule `json:"ipv6,omitempty"`
}

// FirewallRuleRequest is the input shape for adding a firewall rule.
type FirewallRuleRequest struct {
	Action    string `json:"action"`
	Protocol  string `json:"protocol"`
	Port      string `json:"port"`
	Source    string `json:"source"`
	Direction string `json:"direction"`
}

// Service manages firewall rules on remote servers over SSH.
type Service struct {
	srvRepo modserver.Repo
	pool    *modssh.Pool
	authSvc transport.AESKeyProvider
	hosts   transport.HostKeyStore
}

// NewService creates a new firewall service.
func NewService(
	srvRepo modserver.Repo,
	pool *modssh.Pool,
	authSvc transport.AESKeyProvider,
	hosts transport.HostKeyStore,
) *Service {
	return &Service{srvRepo: srvRepo, pool: pool, authSvc: authSvc, hosts: hosts}
}

var (
	portSpecPattern          = regexp.MustCompile(`^\d{1,5}(?:[:-]\d{1,5})?$`)
	sourceV4OrV6Pattern      = regexp.MustCompile(`^[A-Za-z0-9:./_-]+$`)
	ufwRulePattern           = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s+(ALLOW|DENY|REJECT|DROP)\s+(IN|OUT|FORWARD)\s+(.+)$`)
	iptablesChainPattern     = regexp.MustCompile(`^Chain\s+(\S+)\s+\(policy\s+(\S+)`)
	iptablesRuleLinePattern  = regexp.MustCompile(`^(\d+)\s+(ACCEPT|DROP|REJECT|RETURN|LOG|MARK|DNAT|SNAT|MASQUERADE|REDIRECT|QUEUE)\s+(\S+)\s+--\s+(\S+)\s+(\S+)\s*(.*)$`)
	ip6tablesRuleLinePattern = iptablesRuleLinePattern
	portValuePattern         = regexp.MustCompile(`(?i)(?:dpt|spt|dpts|spts):([0-9,:-]+)`)
)

func (s *Service) getSSHClient(ctx context.Context, serverID int) (transport.SSHExecuter, error) {
	if s.pool == nil || s.authSvc == nil || s.hosts == nil || s.srvRepo == nil {
		return nil, fmt.Errorf("firewall service is not configured")
	}

	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server %d not found: %w", serverID, err)
	}

	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	password, err := decryptCredential(aesKey, srv.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt password: %w", err)
	}
	sshKey, err := decryptCredential(aesKey, srv.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt ssh key: %w", err)
	}
	passphrase, err := decryptCredential(aesKey, srv.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt passphrase: %w", err)
	}

	port := srv.Port
	if port == 0 {
		port = 22
	}

	cfg := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
		Timeouts:   modssh.DiscoveryTimeouts,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	hostKeyCallback := s.hosts.MakeHostKeyCallback(serverID)
	return discovery.NewPoolAdapter(s.pool).GetContext(ctx, serverID, cfg, hostKeyCallback)
}

func (s *Service) inspectFirewall(ctx context.Context, serverID int) (*firewallSnapshot, error) {
	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	snap := &firewallSnapshot{}
	snap.ufwStatus, _ = execOutput(ctx, client, "sudo ufw status")
	snap.ufwVerbose, _ = execOutput(ctx, client, "sudo ufw status verbose")
	snap.iptables, _ = execOutput(ctx, client, "sudo iptables -L -n --line-numbers")
	snap.ip6tables, _ = execOutput(ctx, client, "sudo ip6tables -L -n --line-numbers")
	snap.firewalldState, _ = execOutput(ctx, client, "sudo firewall-cmd --state")
	snap.firewalldZone, _ = execOutput(ctx, client, "sudo firewall-cmd --get-default-zone")
	if strings.TrimSpace(snap.firewalldZone) == "" {
		snap.firewalldZone = "public"
	}
	if strings.TrimSpace(snap.firewalldState) == "running" {
		cmd := fmt.Sprintf("sudo firewall-cmd --list-all --zone=%s", shared.ShellQuote(strings.TrimSpace(snap.firewalldZone)))
		snap.firewalldList, _ = execOutput(ctx, client, cmd)
	}

	snap.kind = detectFirewallTypeFromSnapshot(snap)
	return snap, nil
}

// GetFirewallStatus detects the firewall and returns its current status.
func (s *Service) GetFirewallStatus(ctx context.Context, serverID int) (*FirewallStatus, error) {
	snap, err := s.inspectFirewall(ctx, serverID)
	if err != nil {
		return nil, err
	}

	status := &FirewallStatus{Type: snap.kind}
	switch snap.kind {
	case "ufw":
		status.Active = hasUFWActive(snap.ufwStatus)
		status.Default = parseUFWDefaultPolicy(snap.ufwVerbose)
	case "iptables":
		status.Default, status.Active = parseIPTablesStatus(snap.iptables, snap.ip6tables)
	case "firewalld":
		status.Active = strings.TrimSpace(snap.firewalldState) == "running"
		status.Default = strings.TrimSpace(snap.firewalldZone)
	default:
		status.Active = false
		status.Default = ""
	}

	return status, nil
}

// GetFirewallType returns the detected firewall type.
func (s *Service) GetFirewallType(ctx context.Context, serverID int) (string, error) {
	snap, err := s.inspectFirewall(ctx, serverID)
	if err != nil {
		return "", err
	}
	return snap.kind, nil
}

// ListRules lists firewall rules, preferring ufw and falling back to iptables.
func (s *Service) ListRules(ctx context.Context, serverID int) (*FirewallRules, error) {
	snap, err := s.inspectFirewall(ctx, serverID)
	if err != nil {
		return nil, err
	}

	status, err := s.GetFirewallStatus(ctx, serverID)
	if err != nil {
		return nil, err
	}

	result := &FirewallRules{Status: *status}
	switch snap.kind {
	case "ufw":
		result.Rules, result.IPv6 = parseUFWRules(snap.ufwStatus)
	case "iptables":
		result.Rules, _ = parseIPTablesRules(snap.iptables, false)
		if len(result.IPv6) == 0 {
			result.IPv6, _ = parseIPTablesRules(snap.ip6tables, true)
		}
	case "firewalld":
		result.Rules, result.IPv6 = parseFirewalldRules(snap.firewalldList)
	default:
		result.Rules = []FirewallRule{}
		result.IPv6 = []FirewallRule{}
	}

	return result, nil
}

// AddRule adds a firewall rule on the remote server.
func (s *Service) AddRule(ctx context.Context, serverID int, rule FirewallRuleRequest) error {
	normalized, err := normalizeFirewallRuleRequest(rule)
	if err != nil {
		return err
	}

	typeName, err := s.GetFirewallType(ctx, serverID)
	if err != nil {
		return err
	}

	if typeName == "none" {
		return fmt.Errorf("no supported firewall detected")
	}
	if typeName == "firewalld" {
		return fmt.Errorf("firewalld rule management is not supported")
	}

	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	switch typeName {
	case "ufw":
		return s.addUFWRule(ctx, client, normalized)
	case "iptables":
		return s.addIPTablesRule(ctx, client, normalized)
	default:
		return fmt.Errorf("unsupported firewall type: %s", typeName)
	}
}

// DeleteRule deletes a firewall rule by ID.
func (s *Service) DeleteRule(ctx context.Context, serverID int, ruleID string) error {
	typeName, err := s.GetFirewallType(ctx, serverID)
	if err != nil {
		return err
	}

	if typeName == "none" {
		return fmt.Errorf("no supported firewall detected")
	}
	if typeName == "firewalld" {
		return fmt.Errorf("firewalld rule management is not supported")
	}

	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	switch typeName {
	case "ufw":
		return s.deleteUFWRule(ctx, client, ruleID)
	case "iptables":
		return s.deleteIPTablesRule(ctx, client, ruleID)
	default:
		return fmt.Errorf("unsupported firewall type: %s", typeName)
	}
}

// EnableFirewall enables the active firewall.
func (s *Service) EnableFirewall(ctx context.Context, serverID int) error {
	typeName, err := s.GetFirewallType(ctx, serverID)
	if err != nil {
		return err
	}

	if typeName == "none" {
		return fmt.Errorf("no supported firewall detected")
	}

	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	switch typeName {
	case "ufw":
		_, stderr, exitCode, execErr := client.ExecContext(ctx, "sudo ufw --force enable")
		if execErr != nil || exitCode != 0 {
			return fmt.Errorf("enable ufw failed: %w (stderr: %s)", execErr, stderr)
		}
		return nil
	case "iptables":
		return enableIPTables(ctx, client)
	case "firewalld":
		_, stderr, exitCode, execErr := client.ExecContext(ctx, "sudo systemctl start firewalld")
		if execErr != nil || exitCode != 0 {
			return fmt.Errorf("enable firewalld failed: %w (stderr: %s)", execErr, stderr)
		}
		return nil
	default:
		return fmt.Errorf("unsupported firewall type: %s", typeName)
	}
}

// DisableFirewall disables the active firewall.
func (s *Service) DisableFirewall(ctx context.Context, serverID int) error {
	typeName, err := s.GetFirewallType(ctx, serverID)
	if err != nil {
		return err
	}

	if typeName == "none" {
		return fmt.Errorf("no supported firewall detected")
	}

	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	switch typeName {
	case "ufw":
		_, stderr, exitCode, execErr := client.ExecContext(ctx, "sudo ufw --force disable")
		if execErr != nil || exitCode != 0 {
			return fmt.Errorf("disable ufw failed: %w (stderr: %s)", execErr, stderr)
		}
		return nil
	case "iptables":
		return disableIPTables(ctx, client)
	case "firewalld":
		_, stderr, exitCode, execErr := client.ExecContext(ctx, "sudo systemctl stop firewalld")
		if execErr != nil || exitCode != 0 {
			return fmt.Errorf("disable firewalld failed: %w (stderr: %s)", execErr, stderr)
		}
		return nil
	default:
		return fmt.Errorf("unsupported firewall type: %s", typeName)
	}
}

type firewallSnapshot struct {
	kind           string
	ufwStatus      string
	ufwVerbose     string
	iptables       string
	ip6tables      string
	firewalldState string
	firewalldZone  string
	firewalldList  string
}

func execOutput(ctx context.Context, client transport.SSHExecuter, cmd string) (string, error) {
	stdout, stderr, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		if strings.TrimSpace(stderr) != "" {
			return stdout, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr))
		}
		return stdout, err
	}
	return stdout, nil
}

func detectFirewallTypeFromSnapshot(snap *firewallSnapshot) string {
	if snap == nil {
		return "none"
	}
	if strings.Contains(strings.ToLower(snap.ufwStatus), "status:") {
		return "ufw"
	}
	if strings.TrimSpace(snap.firewalldState) == "running" {
		return "firewalld"
	}
	if strings.Contains(strings.ToLower(snap.iptables), "chain ") || strings.Contains(strings.ToLower(snap.ip6tables), "chain ") {
		return "iptables"
	}
	return "none"
}

func hasUFWActive(statusOutput string) bool {
	return strings.Contains(strings.ToLower(statusOutput), "status: active")
}

func parseUFWDefaultPolicy(verbose string) string {
	for _, line := range strings.Split(verbose, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "default:") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "Default:"))
		if idx := strings.Index(line, "("); idx > 0 {
			line = strings.TrimSpace(line[:idx])
		}
		parts := strings.Split(line, ",")
		if len(parts) > 0 {
			first := strings.TrimSpace(parts[0])
			if first != "" {
				return first
			}
		}
	}
	return ""
}

func parseIPTablesStatus(v4, v6 string) (string, bool) {
	policy := ""
	active := false
	if p, ok := parseIPTablesPolicy(v4); ok {
		policy = p
	}
	if policy == "" {
		if p, ok := parseIPTablesPolicy(v6); ok {
			policy = p
		}
	}
	v4Rules, _ := parseIPTablesRules(v4, false)
	v6Rules, _ := parseIPTablesRules(v6, true)
	if len(v4Rules) > 0 || len(v6Rules) > 0 {
		active = true
	}
	if strings.ToUpper(policy) != "ACCEPT" && policy != "" {
		active = true
	}
	return policy, active
}

func parseIPTablesPolicy(output string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		match := iptablesChainPattern.FindStringSubmatch(line)
		if len(match) == 3 {
			return match[2], true
		}
	}
	return "", false
}

func parseUFWRules(output string) ([]FirewallRule, []FirewallRule) {
	var rules []FirewallRule
	var ipv6 []FirewallRule
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "status:") || strings.HasPrefix(line, "to ") || strings.HasPrefix(line, "--") {
			continue
		}
		match := ufwRulePattern.FindStringSubmatch(line)
		if len(match) != 6 {
			continue
		}
		id := match[1]
		target := strings.TrimSpace(match[2])
		action := strings.ToUpper(strings.TrimSpace(match[3]))
		direction := strings.ToUpper(strings.TrimSpace(match[4]))
		source := normalizeAnySource(match[5])
		port, protocol, isIPv6 := parseUFWTarget(target)
		rule := FirewallRule{
			ID:        id,
			Action:    action,
			Protocol:  protocol,
			Port:      port,
			Source:    source,
			Direction: direction,
		}
		if isIPv6 || strings.Contains(strings.ToLower(target), "(v6)") || strings.Contains(strings.ToLower(source), "v6") {
			ipv6 = append(ipv6, rule)
		} else {
			rules = append(rules, rule)
		}
	}
	return rules, ipv6
}

func parseUFWTarget(target string) (port string, protocol string, ipv6 bool) {
	target = strings.TrimSpace(target)
	if strings.Contains(strings.ToLower(target), "(v6)") {
		ipv6 = true
		target = strings.ReplaceAll(target, "(v6)", "")
		target = strings.TrimSpace(target)
	}
	if target == "Anywhere" || target == "Anywhere (v6)" || strings.EqualFold(target, "Anywhere") {
		return "any", "both", ipv6
	}
	if idx := strings.Index(target, "/"); idx > 0 {
		port = strings.TrimSpace(target[:idx])
		protocol = strings.ToLower(strings.TrimSpace(target[idx+1:]))
		if protocol == "" {
			protocol = "both"
		}
		return port, protocol, ipv6
	}
	return target, "both", ipv6
}

func parseIPTablesRules(output string, ipv6 bool) ([]FirewallRule, error) {
	var rules []FirewallRule
	currentChain := "INPUT"
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if match := iptablesChainPattern.FindStringSubmatch(line); len(match) == 3 {
			currentChain = strings.ToUpper(match[1])
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "num ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if !isNumeric(fields[0]) {
			continue
		}
		id := fmt.Sprintf("%s:%s", currentChain, fields[0])
		action := mapIPTablesAction(fields[1])
		protocol := normalizeIPProtocol(fields[2])
		source := normalizeAnySource(fields[4])
		direction := currentChain
		port := parseIPTablesPort(strings.Join(fields[6:], " "))
		if port == "" && len(fields) >= 6 {
			port = parseIPTablesPort(strings.Join(fields[5:], " "))
		}
		if port == "" {
			port = "any"
		}
		if ipv6 && strings.Contains(strings.ToLower(source), "::/") {
			source = normalizeAnySource(source)
		}
		rules = append(rules, FirewallRule{
			ID:        id,
			Action:    action,
			Protocol:  protocol,
			Port:      port,
			Source:    source,
			Direction: direction,
		})
	}
	return rules, nil
}

func parseIPTablesPort(extra string) string {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return ""
	}
	if match := portValuePattern.FindStringSubmatch(extra); len(match) == 2 {
		return match[1]
	}
	if strings.Contains(strings.ToLower(extra), "multiport dports") {
		idx := strings.Index(strings.ToLower(extra), "multiport dports")
		if idx >= 0 {
			value := strings.TrimSpace(extra[idx+len("multiport dports"):])
			fields := strings.Fields(value)
			if len(fields) > 0 {
				return strings.Trim(fields[0], ",")
			}
		}
	}
	return ""
}

func parseFirewalldRules(output string) ([]FirewallRule, []FirewallRule) {
	var rules []FirewallRule
	var ipv6 []FirewallRule
	var idx int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "services:"):
			values := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "services:")))
			for _, svc := range values {
				idx++
				rules = append(rules, FirewallRule{
					ID:        fmt.Sprintf("firewalld:service:%d", idx),
					Action:    "ALLOW",
					Protocol:  "both",
					Port:      svc,
					Source:    "any",
					Direction: "IN",
				})
			}
		case strings.HasPrefix(lower, "ports:"):
			values := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "ports:")))
			for _, port := range values {
				idx++
				proto := "both"
				if parts := strings.SplitN(port, "/", 2); len(parts) == 2 {
					proto = strings.ToLower(parts[1])
				}
				rules = append(rules, FirewallRule{
					ID:        fmt.Sprintf("firewalld:port:%d", idx),
					Action:    "ALLOW",
					Protocol:  proto,
					Port:      port,
					Source:    "any",
					Direction: "IN",
				})
			}
		case strings.Contains(lower, "(v6)"):
			ipv6 = append(ipv6, FirewallRule{
				ID:        fmt.Sprintf("firewalld:v6:%d", idx+1),
				Action:    "ALLOW",
				Protocol:  "both",
				Port:      strings.TrimSpace(line),
				Source:    "any",
				Direction: "IN",
			})
		}
	}
	return rules, ipv6
}

func mapIPTablesAction(target string) string {
	switch strings.ToUpper(strings.TrimSpace(target)) {
	case "ACCEPT":
		return "ALLOW"
	case "DROP":
		return "DROP"
	case "REJECT":
		return "REJECT"
	default:
		return strings.ToUpper(strings.TrimSpace(target))
	}
}

func normalizeIPProtocol(proto string) string {
	proto = strings.ToLower(strings.TrimSpace(proto))
	switch proto {
	case "tcp", "udp":
		return proto
	case "all", "any", "0", "":
		return "both"
	default:
		return proto
	}
}

func normalizeAnySource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "any"
	}
	if source == "0.0.0.0/0" || source == "::/0" || strings.EqualFold(source, "anywhere") || strings.EqualFold(source, "anywhere (v6)") {
		return "any"
	}
	return source
}

func normalizeFirewallRuleRequest(rule FirewallRuleRequest) (FirewallRuleRequest, error) {
	action := strings.ToUpper(strings.TrimSpace(rule.Action))
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
	port, err := normalizePortSpec(rule.Port)
	if err != nil {
		return FirewallRuleRequest{}, err
	}
	if action == "" || protocol == "" || direction == "" {
		return FirewallRuleRequest{}, fmt.Errorf("action, protocol, port, and direction are required")
	}
	if !validAction(action) {
		return FirewallRuleRequest{}, fmt.Errorf("invalid action: %s", action)
	}
	if !validProtocol(protocol) {
		return FirewallRuleRequest{}, fmt.Errorf("invalid protocol: %s", protocol)
	}
	if !validDirection(direction) {
		return FirewallRuleRequest{}, fmt.Errorf("invalid direction: %s", direction)
	}
	if !validSource(rule.Source) {
		return FirewallRuleRequest{}, fmt.Errorf("invalid source: %s", rule.Source)
	}
	if rule.Source == "" {
		rule.Source = "any"
	}
	return FirewallRuleRequest{
		Action:    action,
		Protocol:  protocol,
		Port:      port,
		Source:    strings.TrimSpace(rule.Source),
		Direction: direction,
	}, nil
}

func normalizePortSpec(port string) (string, error) {
	port = strings.TrimSpace(port)
	if port == "" {
		return "", fmt.Errorf("port is required")
	}
	if !portSpecPattern.MatchString(port) {
		return "", fmt.Errorf("invalid port: %s", port)
	}
	parts := strings.FieldsFunc(port, func(r rune) bool { return r == '-' || r == ':' })
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid port: %s", port)
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil || start < 1 || start > 65535 {
		return "", fmt.Errorf("invalid port: %s", port)
	}
	if len(parts) == 1 {
		return strconv.Itoa(start), nil
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil || end < 1 || end > 65535 || start > end {
		return "", fmt.Errorf("invalid port range: %s", port)
	}
	return fmt.Sprintf("%d:%d", start, end), nil
}

func validAction(action string) bool {
	switch strings.ToUpper(action) {
	case "ALLOW", "DENY", "REJECT", "DROP":
		return true
	default:
		return false
	}
}

func validProtocol(protocol string) bool {
	switch strings.ToLower(protocol) {
	case "tcp", "udp", "both":
		return true
	default:
		return false
	}
}

func validDirection(direction string) bool {
	switch strings.ToUpper(direction) {
	case "IN", "OUT", "FORWARD":
		return true
	default:
		return false
	}
}

func validSource(source string) bool {
	source = strings.TrimSpace(source)
	if source == "" || strings.EqualFold(source, "any") {
		return true
	}
	if ip := net.ParseIP(source); ip != nil {
		return true
	}
	if _, _, err := net.ParseCIDR(source); err == nil {
		return true
	}
	return sourceV4OrV6Pattern.MatchString(source)
}

func addRuleCommands(req FirewallRuleRequest) []string {
	protocols := []string{req.Protocol}
	if req.Protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}
	return protocols
}

func (s *Service) addUFWRule(ctx context.Context, client transport.SSHExecuter, req FirewallRuleRequest) error {
	action := ufwAction(req.Action)
	for _, proto := range addRuleCommands(req) {
		cmd := buildUFWCommand(action, req, proto)
		_, stderr, exitCode, err := client.ExecContext(ctx, cmd)
		if err != nil || exitCode != 0 {
			return fmt.Errorf("ufw rule add failed: %w (stderr: %s)", err, stderr)
		}
	}
	return nil
}

func buildUFWCommand(action string, req FirewallRuleRequest, proto string) string {
	port := shared.ShellQuote(req.Port)
	source := strings.TrimSpace(req.Source)
	direction := strings.ToUpper(req.Direction)
	quotedProto := shared.ShellQuote(proto)
	if direction == "FORWARD" {
		if source == "" || strings.EqualFold(source, "any") {
			return fmt.Sprintf("ufw route %s %s proto %s", action, port, quotedProto)
		}
		return fmt.Sprintf("ufw route %s from %s to any port %s proto %s", action, shared.ShellQuote(source), port, quotedProto)
	}
	if direction == "OUT" {
		if source == "" || strings.EqualFold(source, "any") {
			return fmt.Sprintf("ufw %s out to any port %s proto %s", action, port, quotedProto)
		}
		return fmt.Sprintf("ufw %s out from %s to any port %s proto %s", action, shared.ShellQuote(source), port, quotedProto)
	}
	if source == "" || strings.EqualFold(source, "any") {
		if proto == "tcp" || proto == "udp" {
			return fmt.Sprintf("ufw %s %s/%s", action, port, quotedProto)
		}
		return fmt.Sprintf("ufw %s %s", action, port)
	}
	if proto == "tcp" || proto == "udp" {
		return fmt.Sprintf("ufw %s from %s to any port %s proto %s", action, shared.ShellQuote(source), port, quotedProto)
	}
	return fmt.Sprintf("ufw %s from %s to any port %s", action, shared.ShellQuote(source), port)
}

func ufwAction(action string) string {
	switch strings.ToUpper(action) {
	case "ALLOW":
		return "allow"
	case "DENY", "DROP":
		return "deny"
	case "REJECT":
		return "reject"
	default:
		return strings.ToLower(action)
	}
}

func (s *Service) addIPTablesRule(ctx context.Context, client transport.SSHExecuter, req FirewallRuleRequest) error {
	chain := iptablesChain(req.Direction)
	if chain == "" {
		return fmt.Errorf("invalid direction: %s", req.Direction)
	}
	quotedAction := iptablesAction(req.Action)
	protocols := addRuleCommands(req)
	for _, proto := range protocols {
		cmd := buildIPTablesCommand(chain, quotedAction, req, proto)
		_, stderr, exitCode, err := client.ExecContext(ctx, cmd)
		if err != nil || exitCode != 0 {
			return fmt.Errorf("iptables rule add failed: %w (stderr: %s)", err, stderr)
		}
	}
	return nil
}

func buildIPTablesCommand(chain, action string, req FirewallRuleRequest, proto string) string {
	parts := []string{"iptables", "-A", chain}
	if src := strings.TrimSpace(req.Source); src != "" && !strings.EqualFold(src, "any") {
		parts = append(parts, "-s", shared.ShellQuote(src))
	}
	parts = append(parts, "-p", shared.ShellQuote(proto), "--dport", shared.ShellQuote(req.Port), "-j", shared.ShellQuote(action))
	return strings.Join(parts, " ")
}

func iptablesChain(direction string) string {
	switch strings.ToUpper(strings.TrimSpace(direction)) {
	case "IN":
		return "INPUT"
	case "OUT":
		return "OUTPUT"
	case "FORWARD":
		return "FORWARD"
	default:
		return ""
	}
}

func iptablesAction(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "ALLOW":
		return "ACCEPT"
	case "DENY":
		return "DROP"
	case "REJECT":
		return "REJECT"
	case "DROP":
		return "DROP"
	default:
		return strings.ToUpper(strings.TrimSpace(action))
	}
}

func (s *Service) deleteUFWRule(ctx context.Context, client transport.SSHExecuter, ruleID string) error {
	line, err := parseRuleLine(ruleID)
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf("sudo ufw delete %d", line)
	_, stderr, exitCode, execErr := client.ExecContext(ctx, cmd)
	if execErr != nil || exitCode != 0 {
		return fmt.Errorf("ufw rule delete failed: %w (stderr: %s)", execErr, stderr)
	}
	return nil
}

func (s *Service) deleteIPTablesRule(ctx context.Context, client transport.SSHExecuter, ruleID string) error {
	chain, line, err := parseIPTablesRuleID(ruleID)
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf("sudo iptables -D %s %d", chain, line)
	_, stderr, exitCode, execErr := client.ExecContext(ctx, cmd)
	if execErr != nil || exitCode != 0 {
		return fmt.Errorf("iptables rule delete failed: %w (stderr: %s)", execErr, stderr)
	}
	return nil
}

func parseRuleLine(ruleID string) (int, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return 0, fmt.Errorf("rule id is required")
	}
	if strings.Contains(ruleID, ":") {
		parts := strings.Split(ruleID, ":")
		ruleID = parts[len(parts)-1]
	}
	line, err := strconv.Atoi(ruleID)
	if err != nil || line < 1 {
		return 0, fmt.Errorf("invalid rule id: %s", ruleID)
	}
	return line, nil
}

func parseIPTablesRuleID(ruleID string) (string, int, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return "", 0, fmt.Errorf("rule id is required")
	}
	parts := strings.Split(ruleID, ":")
	switch len(parts) {
	case 1:
		line, err := strconv.Atoi(parts[0])
		if err != nil || line < 1 {
			return "", 0, fmt.Errorf("invalid rule id: %s", ruleID)
		}
		return "INPUT", line, nil
	case 2:
		chain := strings.ToUpper(strings.TrimSpace(parts[0]))
		line, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || line < 1 {
			return "", 0, fmt.Errorf("invalid rule id: %s", ruleID)
		}
		if iptablesChain(chain) == "" {
			return "", 0, fmt.Errorf("invalid chain: %s", chain)
		}
		return chain, line, nil
	default:
		chain := strings.ToUpper(strings.TrimSpace(parts[len(parts)-2]))
		line, err := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1]))
		if err != nil || line < 1 {
			return "", 0, fmt.Errorf("invalid rule id: %s", ruleID)
		}
		if iptablesChain(chain) == "" {
			return "", 0, fmt.Errorf("invalid chain: %s", chain)
		}
		return chain, line, nil
	}
}

func enableIPTables(ctx context.Context, client transport.SSHExecuter) error {
	cmd := `if [ -f /etc/iptables/rules.v4 ]; then sudo iptables-restore < /etc/iptables/rules.v4; elif [ -f /etc/sysconfig/iptables ]; then sudo iptables-restore < /etc/sysconfig/iptables; else exit 1; fi`
	_, stderr, exitCode, err := client.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("enable iptables failed: %w (stderr: %s)", err, stderr)
	}
	return nil
}

func disableIPTables(ctx context.Context, client transport.SSHExecuter) error {
	cmd := "sudo iptables -P INPUT ACCEPT && sudo iptables -P FORWARD ACCEPT && sudo iptables -P OUTPUT ACCEPT && sudo iptables -F && sudo iptables -t nat -F && sudo iptables -t mangle -F"
	_, stderr, exitCode, err := client.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("disable iptables failed: %w (stderr: %s)", err, stderr)
	}
	return nil
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

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
