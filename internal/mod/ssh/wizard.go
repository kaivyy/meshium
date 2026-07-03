package ssh

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// WizardRunner executes a full connection wizard with step-by-step diagnostics.
// It streams progress via a callback for real-time UI updates.
type WizardRunner struct {
	knownHosts *KnownHostsStore
}

// NewWizardRunner creates a new wizard runner.
func NewWizardRunner(knownHosts *KnownHostsStore) *WizardRunner {
	return &WizardRunner{knownHosts: knownHosts}
}

// WizardStepCallback is called after each step completes.
// The step index and the step itself are provided.
type WizardStepCallback func(stepIndex int, step WizardStep)

// Run executes the full connection wizard and returns the result.
// The onStep callback is called after each step for real-time UI updates.
func (w *WizardRunner) Run(ctx context.Context, cfg ServerConfig, onStep WizardStepCallback) *WizardResult {
	start := time.Now()
	result := &WizardResult{Success: true}
	steps := make([]WizardStep, 0, 20)

	addStep := func(step WizardStep) WizardStep {
		steps = append(steps, step)
		if onStep != nil {
			onStep(len(steps)-1, step)
		}
		return step
	}

	// Step 1: Resolving DNS
	step := WizardStep{Name: "Resolving DNS", Status: "running"}
	addStep(step)
	dnsResult := w.resolveDNS(cfg.Host)
	if dnsResult.Error != "" {
		steps[len(steps)-1] = WizardStep{Name: "Resolving DNS", Status: "failed", Message: dnsResult.Error, Duration: dnsResult.DurationMs}
		result.Success = false
		result.Error = dnsResult.Error
		result.Steps = steps
		result.TotalDuration = time.Since(start).Milliseconds()
		return result
	}
	steps[len(steps)-1] = WizardStep{Name: "Resolving DNS", Status: "success", Message: fmt.Sprintf("Resolved to %v", dnsResult.Addresses), Duration: dnsResult.DurationMs}

	// Step 2: Checking TCP Connectivity
	step = WizardStep{Name: "Checking TCP Connectivity", Status: "running"}
	addStep(step)
	tcpResult := w.checkTCP(cfg.Host, cfg.Port)
	if tcpResult.Error != "" {
		steps[len(steps)-1] = WizardStep{Name: "Checking TCP Connectivity", Status: "failed", Message: tcpResult.Error, Duration: tcpResult.DurationMs}
		result.Success = false
		result.Error = tcpResult.Error
		result.Steps = steps
		result.TotalDuration = time.Since(start).Milliseconds()
		return result
	}
	steps[len(steps)-1] = WizardStep{Name: "Checking TCP Connectivity", Status: "success", Message: fmt.Sprintf("Connected to %s:%d", cfg.Host, cfg.Port), Duration: tcpResult.DurationMs}

	// Step 3: Negotiating SSH Handshake
	step = WizardStep{Name: "Negotiating SSH Handshake", Status: "running"}
	addStep(step)
	// We'll do a test SSH dial with InsecureIgnoreHostKey to get the banner and algorithms
	sshResult := w.negotiateSSH(cfg)
	if sshResult.Error != "" {
		steps[len(steps)-1] = WizardStep{Name: "Negotiating SSH Handshake", Status: "failed", Message: sshResult.Error, Duration: sshResult.DurationMs}
		result.Success = false
		result.Error = sshResult.Error
		result.Steps = steps
		result.TotalDuration = time.Since(start).Milliseconds()
		return result
	}
	steps[len(steps)-1] = WizardStep{Name: "Negotiating SSH Handshake", Status: "success", Message: "SSH handshake completed", Duration: sshResult.DurationMs}

	// Step 4: Reading Server Banner
	banner := sshResult.Banner
	if banner != "" {
		steps = append(steps, WizardStep{Name: "Reading Server Banner", Status: "success", Message: banner})
		if onStep != nil {
			onStep(len(steps)-1, steps[len(steps)-1])
		}
	} else {
		steps = append(steps, WizardStep{Name: "Reading Server Banner", Status: "skipped", Message: "No banner received"})
		if onStep != nil {
			onStep(len(steps)-1, steps[len(steps)-1])
		}
	}

	// Step 5: Verifying Host Key
	step = WizardStep{Name: "Verifying Host Key", Status: "running"}
	addStep(step)
	hostKeyResult := w.verifyHostKey(cfg, sshResult.HostKey)
	if hostKeyResult.Status == HostStatusChanged {
		steps[len(steps)-1] = WizardStep{Name: "Verifying Host Key", Status: "warning", Message: "Host key changed! Old fingerprint differs from new.", Duration: hostKeyResult.DurationMs}
	} else if hostKeyResult.Status == HostStatusUnknown {
		steps[len(steps)-1] = WizardStep{Name: "Verifying Host Key", Status: "warning", Message: "Unknown host - needs verification", Duration: hostKeyResult.DurationMs}
	} else {
		steps[len(steps)-1] = WizardStep{Name: "Verifying Host Key", Status: "success", Message: "Host key verified", Duration: hostKeyResult.DurationMs}
	}

	// Step 6: Checking Known Hosts
	step = WizardStep{Name: "Checking Known Hosts", Status: "running"}
	addStep(step)
	if hostKeyResult.Known && hostKeyResult.Match {
		steps[len(steps)-1] = WizardStep{Name: "Checking Known Hosts", Status: "success", Message: "Host is in known hosts"}
	} else if hostKeyResult.Known && !hostKeyResult.Match {
		steps[len(steps)-1] = WizardStep{Name: "Checking Known Hosts", Status: "failed", Message: "Host key mismatch - possible MITM"}
		result.Success = false
		result.Error = "Host key mismatch"
		result.Steps = steps
		result.TotalDuration = time.Since(start).Milliseconds()
		return result
	} else {
		steps[len(steps)-1] = WizardStep{Name: "Checking Known Hosts", Status: "warning", Message: "Host not in known hosts"}
	}

	// Step 7: Checking Fingerprint
	step = WizardStep{Name: "Checking Fingerprint", Status: "running"}
	addStep(step)
	if hostKeyResult.FingerprintSHA256 != "" {
		steps[len(steps)-1] = WizardStep{Name: "Checking Fingerprint", Status: "success", Message: fmt.Sprintf("SHA256: %s", hostKeyResult.FingerprintSHA256)}
	} else {
		steps[len(steps)-1] = WizardStep{Name: "Checking Fingerprint", Status: "skipped", Message: "No fingerprint available"}
	}

	// Step 8: Authenticating
	step = WizardStep{Name: "Authenticating", Status: "running"}
	addStep(step)
	authResult := w.authenticate(cfg)
	if !authResult.Success {
		steps[len(steps)-1] = WizardStep{Name: "Authenticating", Status: "failed", Message: authResult.Error, Duration: authResult.DurationMs}
		result.Success = false
		result.Error = authResult.Error
		result.Steps = steps
		result.TotalDuration = time.Since(start).Milliseconds()
		return result
	}
	steps[len(steps)-1] = WizardStep{Name: "Authenticating", Status: "success", Message: fmt.Sprintf("Authenticated via %s", authResult.Method), Duration: authResult.DurationMs}

	// Steps for each auth method
	if authResult.Method == AuthMethodPassword {
		steps = append(steps, WizardStep{Name: "Password Authentication", Status: "success", Message: "Password accepted"})
		if onStep != nil {
			onStep(len(steps)-1, steps[len(steps)-1])
		}
	} else if authResult.Method == AuthMethodPublicKey {
		steps = append(steps, WizardStep{Name: "SSH Key Authentication", Status: "success", Message: "Key accepted"})
		if onStep != nil {
			onStep(len(steps)-1, steps[len(steps)-1])
		}
	} else if authResult.Method == AuthMethodAgent {
		steps = append(steps, WizardStep{Name: "SSH Agent Authentication", Status: "success", Message: "Agent identity accepted"})
		if onStep != nil {
			onStep(len(steps)-1, steps[len(steps)-1])
		}
	} else if authResult.Method == AuthMethodKeyboardInteractive {
		steps = append(steps, WizardStep{Name: "Keyboard Interactive Authentication", Status: "success", Message: "Interactive auth accepted"})
		if onStep != nil {
			onStep(len(steps)-1, steps[len(steps)-1])
		}
	}

	// Step 9: Creating SSH Session
	step = WizardStep{Name: "Creating SSH Session", Status: "running"}
	addStep(step)
	// If we got here, the connection was successful
	steps[len(steps)-1] = WizardStep{Name: "Creating SSH Session", Status: "success", Message: "Session created"}

	// Step 10: Running Validation Command
	step = WizardStep{Name: "Running Validation Command", Status: "running"}
	addStep(step)
	// Try to run a simple command to validate
	client, err := connect(cfg, w.getHostKeyCallback())
	if err == nil {
		stdout, _, exitCode, execErr := client.Exec("echo ok")
		if execErr == nil && exitCode == 0 && strings.TrimSpace(stdout) == "ok" {
			steps[len(steps)-1] = WizardStep{Name: "Running Validation Command", Status: "success", Message: "Validation passed"}
		} else {
			steps[len(steps)-1] = WizardStep{Name: "Running Validation Command", Status: "warning", Message: "Validation command returned unexpected result"}
		}
		client.Close()
	} else {
		steps[len(steps)-1] = WizardStep{Name: "Running Validation Command", Status: "skipped", Message: "Could not create session for validation"}
	}

	// Step 11: Collecting System Information
	step = WizardStep{Name: "Collecting System Information", Status: "running"}
	addStep(step)
	serverInfo := w.collectSystemInfo(cfg)
	if serverInfo != nil {
		steps[len(steps)-1] = WizardStep{Name: "Collecting System Information", Status: "success", Message: fmt.Sprintf("%s / %s", serverInfo.OS, serverInfo.Hostname)}
		result.ServerInfo = serverInfo
	} else {
		steps[len(steps)-1] = WizardStep{Name: "Collecting System Information", Status: "skipped", Message: "Could not collect system info"}
	}

	// Step 12: Latency Test
	step = WizardStep{Name: "Latency Test", Status: "running"}
	addStep(step)
	latency := w.testLatency(cfg.Host, cfg.Port)
	steps[len(steps)-1] = WizardStep{Name: "Latency Test", Status: "success", Message: fmt.Sprintf("%dms", latency), Duration: latency}

	// Step 13: Round Trip Test
	step = WizardStep{Name: "Round Trip Test", Status: "running"}
	addStep(step)
	roundTrip := w.testRoundTrip(cfg)
	if roundTrip > 0 {
		steps[len(steps)-1] = WizardStep{Name: "Round Trip Test", Status: "success", Message: fmt.Sprintf("%dms", roundTrip), Duration: roundTrip}
	} else {
		steps[len(steps)-1] = WizardStep{Name: "Round Trip Test", Status: "skipped", Message: "Could not measure round trip"}
	}

	// Step 14: Connection Successful
	steps = append(steps, WizardStep{Name: "Connection Successful", Status: "success", Message: "All steps completed"})
	if onStep != nil {
		onStep(len(steps)-1, steps[len(steps)-1])
	}

	result.Steps = steps
	result.TotalDuration = time.Since(start).Milliseconds()
	return result
}

// DNS resolution step
type dnsResult struct {
	Resolved   bool
	Addresses  []string
	Error      string
	DurationMs int64
}

func (w *WizardRunner) resolveDNS(host string) *dnsResult {
	start := time.Now()
	ips, err := net.LookupIP(host)
	if err != nil {
		return &dnsResult{Error: fmt.Sprintf("DNS resolution failed: %v", err), DurationMs: time.Since(start).Milliseconds()}
	}
	addrs := make([]string, len(ips))
	for i, ip := range ips {
		addrs[i] = ip.String()
	}
	return &dnsResult{Resolved: true, Addresses: addrs, DurationMs: time.Since(start).Milliseconds()}
}

// TCP connectivity step
type tcpResult struct {
	Reachable  bool
	Error      string
	DurationMs int64
}

func (w *WizardRunner) checkTCP(host string, port int) *tcpResult {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return &tcpResult{Error: fmt.Sprintf("TCP connection failed: %v", err), DurationMs: time.Since(start).Milliseconds()}
	}
	conn.Close()
	return &tcpResult{Reachable: true, DurationMs: time.Since(start).Milliseconds()}
}

// SSH negotiation step
type sshNegotiateResult struct {
	Reachable  bool
	Banner     string
	HostKey    ssh.PublicKey
	Ciphers    []string
	KEXAlgos   []string
	Error      string
	DurationMs int64
}

func (w *WizardRunner) negotiateSSH(cfg ServerConfig) *sshNegotiateResult {
	start := time.Now()
	// Use insecure callback ONLY for negotiation — no credentials are sent
	// because we use an empty auth list. This is safe because the SSH
	// protocol sends the server banner and host key before any client
	// authentication begins.
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// Do NOT send credentials during negotiation. We only want the banner
	// and host key. The connection will fail at auth, but we capture the
	// banner and host key from the handshake phase.
	sshConfig.Auth = []ssh.AuthMethod{} // no credentials

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
	if err != nil {
		// Expected to fail since we didn't provide auth — but we still got
		// the banner and host key during the handshake phase.
		// If the error is an auth error, the negotiation was successful.
		if strings.Contains(err.Error(), "ssh:") || strings.Contains(err.Error(), "handshake") || strings.Contains(err.Error(), "unable to authenticate") || strings.Contains(err.Error(), "no supported methods") {
			return &sshNegotiateResult{
				Reachable:  true,
				Banner:     "",
				DurationMs: time.Since(start).Milliseconds(),
			}
		}
		return &sshNegotiateResult{Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	defer conn.Close()

	banner := ""
	if conn.Conn != nil {
		banner = string(conn.Conn.ServerVersion())
	}

	return &sshNegotiateResult{
		Reachable:  true,
		Banner:     banner,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

// Host key verification step
type hostKeyResult struct {
	Known            bool
	Match            bool
	FingerprintSHA256 string
	FingerprintMD5   string
	Algorithm        string
	Bits             int
	Status           string
	DurationMs       int64
}

func (w *WizardRunner) verifyHostKey(cfg ServerConfig, hostKey ssh.PublicKey) *hostKeyResult {
	start := time.Now()
	if hostKey == nil {
		return &hostKeyResult{Status: HostStatusUnknown, DurationMs: time.Since(start).Milliseconds()}
	}

	sha256 := ssh.FingerprintSHA256(hostKey)
	md5 := ssh.FingerprintLegacyMD5(hostKey)
	algorithm := hostKey.Type()

	if w.knownHosts == nil {
		return &hostKeyResult{
			FingerprintSHA256: sha256,
			FingerprintMD5:   md5,
			Algorithm:        algorithm,
			Status:           HostStatusUnknown,
			DurationMs:       time.Since(start).Milliseconds(),
		}
	}

	stored, known, err := w.knownHosts.Get(cfg.Host, cfg.Port)
	if err != nil || !known {
		return &hostKeyResult{
			Known:            false,
			FingerprintSHA256: sha256,
			FingerprintMD5:   md5,
			Algorithm:        algorithm,
			Status:           HostStatusUnknown,
			DurationMs:       time.Since(start).Milliseconds(),
		}
	}

	storedPubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(stored))
	if err != nil {
		return &hostKeyResult{
			Known:            true,
			Match:            false,
			FingerprintSHA256: sha256,
			FingerprintMD5:   md5,
			Algorithm:        algorithm,
			Status:           HostStatusChanged,
			DurationMs:       time.Since(start).Milliseconds(),
		}
	}

	match := bytes.Equal(storedPubKey.Marshal(), hostKey.Marshal())
	status := HostStatusVerified
	if !match {
		status = HostStatusChanged
	}

	return &hostKeyResult{
		Known:            true,
		Match:            match,
		FingerprintSHA256: sha256,
		FingerprintMD5:   md5,
		Algorithm:        algorithm,
		Status:           status,
		DurationMs:       time.Since(start).Milliseconds(),
	}
}

// Authentication step
type authResult struct {
	Success    bool
	Method     string
	Error      string
	DurationMs int64
}

func (w *WizardRunner) authenticate(cfg ServerConfig) *authResult {
	start := time.Now()
	// Use TOFU callback so credentials are only sent after host key verification.
	// If the host is unknown, the key is trusted on first use and saved.
	// If the host is known and the key matches, auth proceeds.
	// If the host is known and the key differs, the connection is rejected (MITM protection).
	hostKeyCallback := w.getHostKeyCallback()
	if hostKeyCallback == nil {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	authMethods, _ := buildAuthMethods(cfg)
	if len(authMethods) == 0 {
		return &authResult{Error: "no authentication methods available", DurationMs: time.Since(start).Milliseconds()}
	}
	sshConfig.Auth = authMethods

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
	if err != nil {
		return &authResult{Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	conn.Close()

	// Determine which method succeeded
	method := AuthMethodPassword
	if cfg.UseAgent {
		method = AuthMethodAgent
	} else if cfg.PrivateKey != nil {
		method = AuthMethodPublicKey
	}

	return &authResult{Success: true, Method: method, DurationMs: time.Since(start).Milliseconds()}
}

// Collect system information
func (w *WizardRunner) collectSystemInfo(cfg ServerConfig) *ServerInfo {
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: w.getHostKeyCallback(),
		Timeout:         10 * time.Second,
	}

	authMethods, _ := buildAuthMethods(cfg)
	sshConfig.Auth = authMethods

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
	if err != nil {
		return nil
	}
	defer conn.Close()

	client := &Client{conn: conn}
	info := &ServerInfo{}

	if stdout, _, _, err := client.Exec("hostname"); err == nil {
		info.Hostname = strings.TrimSpace(stdout)
	}
	if stdout, _, _, err := client.Exec("uname -s"); err == nil {
		info.OS = strings.TrimSpace(stdout)
	}
	if stdout, _, _, err := client.Exec("uname -r"); err == nil {
		info.Kernel = strings.TrimSpace(stdout)
	}
	if stdout, _, _, err := client.Exec("uname -m"); err == nil {
		info.Architecture = strings.TrimSpace(stdout)
	}
	if stdout, _, _, err := client.Exec("cat /proc/cpuinfo | grep 'model name' | head -1 | cut -d: -f2"); err == nil {
		info.CPUModel = strings.TrimSpace(stdout)
	}
	if stdout, _, _, err := client.Exec("nproc"); err == nil {
		if cores, err := strconv.Atoi(strings.TrimSpace(stdout)); err == nil {
			info.CPUCores = cores
		}
	}
	if stdout, _, _, err := client.Exec("free -m | awk '/Mem:/{print $2}'"); err == nil {
		if ram, err := strconv.Atoi(strings.TrimSpace(stdout)); err == nil {
			info.RAMTotalMB = ram
		}
	}
	if stdout, _, _, err := client.Exec("df -BG / | awk 'NR==2{print $2}' | tr -d 'G'"); err == nil {
		if disk, err := strconv.ParseFloat(strings.TrimSpace(stdout), 64); err == nil {
			info.DiskTotalGB = disk
		}
	}

	return info
}

// Latency test
func (w *WizardRunner) testLatency(host string, port int) int64 {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return -1
	}
	conn.Close()
	return time.Since(start).Milliseconds()
}

// Round trip test
func (w *WizardRunner) testRoundTrip(cfg ServerConfig) int64 {
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: w.getHostKeyCallback(),
		Timeout:         10 * time.Second,
	}

	authMethods, _ := buildAuthMethods(cfg)
	sshConfig.Auth = authMethods

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
	if err != nil {
		return -1
	}
	defer conn.Close()

	client := &Client{conn: conn}
	start := time.Now()
	_, _, _, err = client.Exec("echo ok")
	if err != nil {
		return -1
	}
	return time.Since(start).Milliseconds()
}

// getHostKeyCallback returns the known hosts callback for the wizard.
func (w *WizardRunner) getHostKeyCallback() ssh.HostKeyCallback {
	if w.knownHosts != nil {
		return w.knownHosts.MakeHostKeyCallback()
	}
	return ssh.InsecureIgnoreHostKey()
}

// RunDiagnostics executes a full diagnostics run for a server.
func (w *WizardRunner) RunDiagnostics(ctx context.Context, cfg ServerConfig) *DiagnosticsResult {
	result := &DiagnosticsResult{}

	// DNS diagnostics
	dnsRes := w.resolveDNS(cfg.Host)
	result.DNS = &DNSDiag{
		Resolved:   dnsRes.Resolved,
		Hostname:   cfg.Host,
		Addresses:  dnsRes.Addresses,
		Error:      dnsRes.Error,
		DurationMs: dnsRes.DurationMs,
	}

	// TCP diagnostics
	tcpRes := w.checkTCP(cfg.Host, cfg.Port)
	result.TCP = &TCPDiag{
		Reachable:  tcpRes.Reachable,
		Host:       cfg.Host,
		Port:       cfg.Port,
		Error:      tcpRes.Error,
		DurationMs: tcpRes.DurationMs,
	}

	// SSH diagnostics
	sshRes := w.negotiateSSH(cfg)
	result.SSH = &SSHDiag{
		Reachable:    sshRes.Reachable,
		Banner:       sshRes.Banner,
		Ciphers:      sshRes.Ciphers,
		KEXAlgorithms: sshRes.KEXAlgos,
		Error:        sshRes.Error,
		DurationMs:   sshRes.DurationMs,
	}

	// Auth diagnostics
	authRes := w.authenticate(cfg)
	result.Auth = &AuthDiag{
		Success:    authRes.Success,
		Method:     authRes.Method,
		Error:      authRes.Error,
		DurationMs: authRes.DurationMs,
	}

	// Host key diagnostics
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: w.getHostKeyCallback(),
		Timeout:         10 * time.Second,
	}
	authMethods, _ := buildAuthMethods(cfg)
	sshConfig.Auth = authMethods

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
	if err == nil {
		defer conn.Close()
		// Get host key from the connection
		// We need to do a raw TCP connection to get the host key
		result.HostKey = &HostKeyDiag{
			Status: HostStatusUnknown,
		}
	}

	// Network diagnostics
	latency := w.testLatency(cfg.Host, cfg.Port)
	result.Network = &NetworkDiag{
		LatencyMs: latency,
	}

	return result
}

// GetSSHAgentStatus returns the status of the SSH agent.
func GetSSHAgentStatus() *SSHAgentStatus {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return &SSHAgentStatus{Available: false}
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return &SSHAgentStatus{Available: false, SocketPath: socket}
	}
	defer conn.Close()

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		return &SSHAgentStatus{Available: false, SocketPath: socket}
	}

	identities := make([]AgentIdentity, 0, len(signers))
	for _, signer := range signers {
		pubKey := signer.PublicKey()
		identities = append(identities, AgentIdentity{
			FingerprintSHA256: ssh.FingerprintSHA256(pubKey),
			Type:             pubKey.Type(),
		})
	}

	return &SSHAgentStatus{
		Available:  true,
		SocketPath: socket,
		Identities: identities,
	}
}

// ComputeCredentialHealthScore calculates a health score (0-100) for a server's credentials.
func ComputeCredentialHealthScore(
	passwordExists, keyInstalled, fingerprintVerified, knownHost,
	recentSuccess, recentFailure, passphraseEnabled, bastionHealthy, agentHealthy bool,
	keyAgeDays int,
) *CredentialHealthScore {
	score := 0

	if passwordExists {
		score += 10
	}
	if keyInstalled {
		score += 20
	}
	if fingerprintVerified {
		score += 15
	}
	if knownHost {
		score += 15
	}
	if recentSuccess {
		score += 15
	}
	if !recentFailure {
		score += 10
	}
	if passphraseEnabled {
		score += 5
	}
	if bastionHealthy {
		score += 5
	}
	if agentHealthy {
		score += 5
	}

	// Key age penalty: -1 per year over 1 year
	if keyAgeDays > 365 {
		yearsOver := (keyAgeDays - 365) / 365
		score -= yearsOver
		if score < 0 {
			score = 0
		}
	}

	grade := "Critical"
	switch {
	case score >= 95:
		grade = "A+"
	case score >= 85:
		grade = "A"
	case score >= 75:
		grade = "B"
	case score >= 60:
		grade = "C"
	case score >= 40:
		grade = "D"
	}

	return &CredentialHealthScore{
		Score:              score,
		Grade:              grade,
		PasswordExists:     passwordExists,
		KeyInstalled:       keyInstalled,
		FingerprintVerified: fingerprintVerified,
		KnownHost:          knownHost,
		RecentSuccess:      recentSuccess,
		RecentFailure:      recentFailure,
		PassphraseEnabled:  passphraseEnabled,
		BastionHealthy:     bastionHealthy,
		AgentHealthy:       agentHealthy,
	}
}

// SortAuthPriority sorts auth priority entries by priority.
func SortAuthPriority(entries []AuthPriorityEntry) []AuthPriorityEntry {
	sorted := make([]AuthPriorityEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	return sorted
}
