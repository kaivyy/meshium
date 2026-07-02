package migration

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshium/internal/shared"
)

// HealthEngine performs comprehensive health checks on source and target servers.
// It supports HTTP, HTTPS, TCP, database, Redis, queue, container, systemd,
// DNS, SSL, and disk health checks, and calculates a composite health score.
type HealthEngine struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
	repo      PipelineRepo
	client    *http.Client
}

// NewHealthEngine creates a new health check engine.
func NewHealthEngine(sourceSSH, targetSSH SSHExecuter, repo PipelineRepo) *HealthEngine {
	return &HealthEngine{
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
		repo:      repo,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				MaxIdleConns:        10,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   true,
				TLSHandshakeTimeout: 5 * time.Second,
				DialContext: (&net.Dialer{
					Timeout: 5 * time.Second,
				}).DialContext,
			},
		},
	}
}

// HealthCheckConfig configures a single health check.
type HealthCheckConfig struct {
	Type     HealthCheckType `json:"type"`
	Target   string          `json:"target"`
	Timeout  time.Duration   `json:"timeout,omitempty"`
	ServerID int             `json:"serverId"`
	UseSource bool           `json:"useSource"`
}

// CheckAll runs all health checks in parallel and returns results with a composite score.
func (e *HealthEngine) CheckAll(ctx context.Context, checks []HealthCheckConfig) ([]HealthCheckResult, HealthScore) {
	results := make([]HealthCheckResult, len(checks))
	var wg sync.WaitGroup

	for i, check := range checks {
		wg.Add(1)
		go func(idx int, cfg HealthCheckConfig) {
			defer wg.Done()
			results[idx] = e.runCheck(ctx, cfg)
		}(i, check)
	}
	wg.Wait()

	score := e.CalculateScore(results)
	return results, score
}

// runCheck dispatches to the appropriate check method.
func (e *HealthEngine) runCheck(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	switch cfg.Type {
	case HealthCheckHTTP, HealthCheckHTTPS:
		return e.checkHTTP(checkCtx, cfg)
	case HealthCheckTCP:
		return e.checkTCP(checkCtx, cfg)
	case HealthCheckDatabase:
		return e.checkDatabase(checkCtx, cfg)
	case HealthCheckRedis:
		return e.checkRedis(checkCtx, cfg)
	case HealthCheckQueue:
		return e.checkQueue(checkCtx, cfg)
	case HealthCheckContainer:
		return e.checkContainer(checkCtx, cfg)
	case HealthCheckSystemd:
		return e.checkSystemd(checkCtx, cfg)
	case HealthCheckDNS:
		return e.checkDNS(checkCtx, cfg)
	case HealthCheckSSL:
		return e.checkSSL(checkCtx, cfg)
	case HealthCheckDisk:
		return e.checkDisk(checkCtx, cfg)
	default:
		return HealthCheckResult{
			CheckType:   cfg.Type,
			CheckTarget: cfg.Target,
			Status:      "unknown",
			HealthScore: 0,
		}
	}
}

// CheckHTTP performs an HTTP/HTTPS health check.
func (e *HealthEngine) CheckHTTP(ctx context.Context, url string, timeout time.Duration) HealthCheckResult {
	return e.checkHTTP(ctx, HealthCheckConfig{
		Type:    HealthCheckHTTP,
		Target:  url,
		Timeout: timeout,
	})
}

func (e *HealthEngine) checkHTTP(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", cfg.Target, nil)
	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}
	req.Header.Set("User-Agent", "Meshium-HealthCheck/1.0")

	resp, err := e.client.Do(req)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "unhealthy"
		result.ErrorMessage = fmt.Sprintf("status code %d", resp.StatusCode)
		result.HealthScore = 20
	}
	return result
}

// CheckTCP performs a TCP connectivity check.
func (e *HealthEngine) CheckTCP(ctx context.Context, host string, port int, timeout time.Duration) HealthCheckResult {
	return e.checkTCP(ctx, HealthCheckConfig{
		Type:    HealthCheckTCP,
		Target:  net.JoinHostPort(host, strconv.Itoa(port)),
		Timeout: timeout,
	})
}

func (e *HealthEngine) checkTCP(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Target)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}
	conn.Close()

	result.Status = "healthy"
	result.HealthScore = e.scoreFromLatency(elapsed)
	return result
}

// CheckRedis performs a Redis ping check via SSH.
func (e *HealthEngine) CheckRedis(ctx context.Context, ssh SSHExecuter, host string, port int) HealthCheckResult {
	return e.checkRedis(ctx, HealthCheckConfig{
		Type:     HealthCheckRedis,
		Target:   fmt.Sprintf("%s:%d", host, port),
		UseSource: false,
	})
}

func (e *HealthEngine) checkRedis(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	ssh := e.targetSSH
	if cfg.UseSource {
		ssh = e.sourceSSH
	}

	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	host := "127.0.0.1"
	port := 6379
	if cfg.Target != "" {
		parts := strings.Split(cfg.Target, ":")
		if len(parts) >= 1 {
			host = parts[0]
		}
		if len(parts) >= 2 {
			port, _ = strconv.Atoi(parts[1])
		}
	}

	cmd := fmt.Sprintf("redis-cli -h %s -p %d ping", shared.ShellQuote(host), port)
	output, _, _, err := ssh.ExecContext(ctx, cmd)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	if strings.Contains(output, "PONG") {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "unhealthy"
		result.ErrorMessage = "redis did not respond with PONG"
		result.HealthScore = 20
	}
	return result
}

// CheckDatabase performs a database connectivity check via SSH.
func (e *HealthEngine) CheckDatabase(ctx context.Context, ssh SSHExecuter, dbType, host string, port int) HealthCheckResult {
	return e.checkDatabase(ctx, HealthCheckConfig{
		Type:     HealthCheckDatabase,
		Target:   fmt.Sprintf("%s://%s:%d", dbType, host, port),
		UseSource: false,
	})
}

func (e *HealthEngine) checkDatabase(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	ssh := e.targetSSH
	if cfg.UseSource {
		ssh = e.sourceSSH
	}

	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	parts := strings.SplitN(cfg.Target, "://", 2)
	dbType := "mysql"
	if len(parts) == 2 {
		dbType = parts[0]
	}

	var cmd string
	switch dbType {
	case "mysql", "mariadb":
		cmd = "mysql -e 'SELECT 1' 2>&1"
	case "postgres", "postgresql":
		cmd = "psql -c 'SELECT 1' 2>&1"
	case "mongodb":
		cmd = "mongosh --eval 'db.runCommand({ping:1})' 2>&1"
	default:
		cmd = fmt.Sprintf("echo 'Unsupported database type: %s'", dbType)
	}

	output, _, _, err := ssh.ExecContext(ctx, cmd)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	if strings.Contains(output, "1") || strings.Contains(output, "ok") {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "unhealthy"
		result.ErrorMessage = "database query did not return expected result"
		result.HealthScore = 20
	}
	return result
}

// CheckContainer checks a Docker container's health status via SSH.
func (e *HealthEngine) CheckContainer(ctx context.Context, ssh SSHExecuter, containerName string) HealthCheckResult {
	return e.checkContainer(ctx, HealthCheckConfig{
		Type:    HealthCheckContainer,
		Target:  containerName,
	})
}

func (e *HealthEngine) checkContainer(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	ssh := e.targetSSH
	if cfg.UseSource {
		ssh = e.sourceSSH
	}

	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	cmd := fmt.Sprintf("docker inspect --format='{{.State.Status}} {{.State.Health.Status}}' %s 2>&1", shared.ShellQuote(cfg.Target))
	output, _, _, err := ssh.ExecContext(ctx, cmd)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	output = strings.TrimSpace(output)
	if strings.Contains(output, "running") && (strings.Contains(output, "healthy") || !strings.Contains(output, "unhealthy")) {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "unhealthy"
		result.ErrorMessage = fmt.Sprintf("container state: %s", output)
		result.HealthScore = 20
	}
	return result
}

// CheckSystemd checks a systemd service's active status via SSH.
func (e *HealthEngine) CheckSystemd(ctx context.Context, ssh SSHExecuter, serviceName string) HealthCheckResult {
	return e.checkSystemd(ctx, HealthCheckConfig{
		Type:    HealthCheckSystemd,
		Target:  serviceName,
	})
}

func (e *HealthEngine) checkSystemd(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	ssh := e.targetSSH
	if cfg.UseSource {
		ssh = e.sourceSSH
	}

	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	cmd := fmt.Sprintf("systemctl is-active %s 2>&1", shared.ShellQuote(cfg.Target))
	output, _, _, err := ssh.ExecContext(ctx, cmd)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	output = strings.TrimSpace(output)
	if output == "active" {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "unhealthy"
		result.ErrorMessage = fmt.Sprintf("service state: %s", output)
		result.HealthScore = 20
	}
	return result
}

// CheckDNS performs a DNS resolution check.
func (e *HealthEngine) CheckDNS(ctx context.Context, hostname string) HealthCheckResult {
	return e.checkDNS(ctx, HealthCheckConfig{
		Type:   HealthCheckDNS,
		Target: hostname,
	})
}

func (e *HealthEngine) checkDNS(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	// Use context-aware resolver
	resolver := net.Resolver{}
	addrs, err := resolver.LookupHost(ctx, cfg.Target)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if ctx.Err() != nil {
		result.Status = "error"
		result.ErrorMessage = ctx.Err().Error()
		result.HealthScore = 0
		return result
	}

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	if len(addrs) > 0 {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "unhealthy"
		result.ErrorMessage = "no DNS records found"
		result.HealthScore = 20
	}
	return result
}

// CheckSSL performs an SSL certificate expiry check.
func (e *HealthEngine) CheckSSL(ctx context.Context, hostname string, port int) HealthCheckResult {
	return e.checkSSL(ctx, HealthCheckConfig{
		Type:   HealthCheckSSL,
		Target: fmt.Sprintf("%s:%d", hostname, port),
	})
}

func (e *HealthEngine) checkSSL(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	target := cfg.Target
	if !strings.Contains(target, ":") {
		target = target + ":443"
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, &tls.Config{
		InsecureSkipVerify: true,
	})
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		result.Status = "unhealthy"
		result.ErrorMessage = "no certificates found"
		result.HealthScore = 20
		return result
	}

	expiresIn := time.Until(certs[0].NotAfter)
	if expiresIn < 0 {
		result.Status = "unhealthy"
		result.ErrorMessage = fmt.Sprintf("certificate expired %s ago", -expiresIn.Round(time.Hour))
		result.HealthScore = 0
	} else if expiresIn < 7*24*time.Hour {
		result.Status = "degraded"
		result.ErrorMessage = fmt.Sprintf("certificate expires in %s", expiresIn.Round(time.Hour))
		result.HealthScore = 50
	} else {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	}
	return result
}

// CheckDiskWritable checks if a path is writable on the remote server.
func (e *HealthEngine) CheckDiskWritable(ctx context.Context, ssh SSHExecuter, path string) HealthCheckResult {
	return e.checkDisk(ctx, HealthCheckConfig{
		Type:    HealthCheckDisk,
		Target:  path,
	})
}

func (e *HealthEngine) checkDisk(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	ssh := e.targetSSH
	if cfg.UseSource {
		ssh = e.sourceSSH
	}

	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	testFile := fmt.Sprintf("%s/.meshium-health-check-%d", cfg.Target, time.Now().UnixNano())
	cmd := fmt.Sprintf("touch %s && rm %s && df -h %s 2>&1", shared.ShellQuote(testFile), shared.ShellQuote(testFile), shared.ShellQuote(cfg.Target))
	output, _, _, err := ssh.ExecContext(ctx, cmd)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	lines := strings.Split(output, "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 5 {
			usePct := strings.TrimSuffix(fields[4], "%")
			if pct, e := strconv.Atoi(usePct); e == nil {
				if pct < 90 {
					result.Status = "healthy"
					result.HealthScore = float64(100-pct) / 100.0 * 100
				} else {
					result.Status = "degraded"
					result.ErrorMessage = fmt.Sprintf("disk usage %d%%", pct)
					result.HealthScore = float64(100-pct) / 100.0 * 50
				}
			}
		}
	} else {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	}
	return result
}

// checkQueue checks queue health via SSH.
func (e *HealthEngine) checkQueue(ctx context.Context, cfg HealthCheckConfig) HealthCheckResult {
	ssh := e.targetSSH
	if cfg.UseSource {
		ssh = e.sourceSSH
	}

	start := time.Now()
	result := HealthCheckResult{
		CheckType:   cfg.Type,
		CheckTarget: cfg.Target,
	}

	// Default to BullMQ check via Redis
	cmd := fmt.Sprintf("redis-cli LLEN bull:%s:active 2>&1", shared.ShellQuote(cfg.Target))
	output, _, _, err := ssh.ExecContext(ctx, cmd)
	elapsed := time.Since(start)
	result.ResponseTimeMs = elapsed.Milliseconds()

	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return result
	}

	activeCount, parseErr := strconv.Atoi(strings.TrimSpace(output))
	if parseErr != nil {
		result.Status = "healthy"
		result.HealthScore = e.scoreFromLatency(elapsed)
	} else {
		result.Status = "healthy"
		if activeCount > 1000 {
			result.Status = "degraded"
			result.ErrorMessage = fmt.Sprintf("%d active jobs in queue", activeCount)
			result.HealthScore = 50
		} else {
			result.HealthScore = e.scoreFromLatency(elapsed)
		}
	}
	return result
}

// CalculateScore computes a composite health score from multiple check results.
func (e *HealthEngine) CalculateScore(results []HealthCheckResult) HealthScore {
	if len(results) == 0 {
		return HealthScore{}
	}

	score := HealthScore{
		ChecksTotal: len(results),
	}

	var totalScore float64
	var totalLatency float64

	for _, r := range results {
		totalScore += r.HealthScore
		totalLatency += float64(r.ResponseTimeMs)
		if r.Status == "healthy" || r.Status == "degraded" {
			score.ChecksPassed++
		} else {
			score.ChecksFailed++
		}
	}

	score.Score = totalScore / float64(len(results))
	score.AvgResponseMs = totalLatency / float64(len(results))
	score.ErrorRate = float64(score.ChecksFailed) / float64(score.ChecksTotal)
	return score
}

// scoreFromLatency converts latency to a health score (0-100).
// Lower latency = higher score.
func (e *HealthEngine) scoreFromLatency(elapsed time.Duration) float64 {
	ms := float64(elapsed.Milliseconds())
	switch {
	case ms < 50:
		return 100
	case ms < 100:
		return 95
	case ms < 200:
		return 90
	case ms < 500:
		return 80
	case ms < 1000:
		return 70
	case ms < 2000:
		return 60
	case ms < 5000:
		return 50
	default:
		return 20
	}
}
