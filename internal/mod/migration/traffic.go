package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"meshium/internal/shared"
)

// TrafficSwitchEngine handles traffic switching between source and target servers.
// It supports Cloudflare, Nginx, Traefik, HAProxy, Caddy, Docker, and DNS providers.
type TrafficSwitchEngine struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
	repo      PipelineRepo
	client    *http.Client
}

// NewTrafficSwitchEngine creates a new traffic switch engine.
func NewTrafficSwitchEngine(sourceSSH, targetSSH SSHExecuter, repo PipelineRepo) *TrafficSwitchEngine {
	return &TrafficSwitchEngine{
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
		repo:      repo,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Switch switches traffic from source to target server.
func (e *TrafficSwitchEngine) Switch(ctx context.Context, migrationID int, cfg TrafficSwitchConfig) error {
	// Record original config before switching
	original, err := e.GetProviderConfig(ctx, cfg.Provider, cfg.MigrationID)
	if err != nil {
		return fmt.Errorf("get current config: %w", err)
	}
	cfg.OriginalConfig = original

	// Store in database
	configID, err := e.repo.CreateTrafficSwitchConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("save traffic switch config: %w", err)
	}
	cfg.ID = configID

	var switchErr error
	switch cfg.Provider {
	case TrafficProviderCloudflare:
		switchErr = e.switchCloudflare(ctx, &cfg)
	case TrafficProviderNginx:
		switchErr = e.switchNginx(ctx, &cfg)
	case TrafficProviderTraefik:
		switchErr = e.switchTraefik(ctx, &cfg)
	case TrafficProviderHAProxy:
		switchErr = e.switchHAProxy(ctx, &cfg)
	case TrafficProviderCaddy:
		switchErr = e.switchCaddy(ctx, &cfg)
	case TrafficProviderDocker:
		switchErr = e.switchDocker(ctx, &cfg)
	case TrafficProviderDNS:
		switchErr = e.switchDNS(ctx, &cfg)
	default:
		return fmt.Errorf("unsupported traffic provider: %s", cfg.Provider)
	}

	if switchErr != nil {
		_ = e.repo.UpdateTrafficSwitchState(ctx, configID, "failed")
		return switchErr
	}

	cfg.SwitchState = "switched"
	if updater, ok := e.repo.(interface {
		UpdateTrafficSwitchConfig(context.Context, TrafficSwitchConfig) error
	}); ok {
		if err := updater.UpdateTrafficSwitchConfig(ctx, cfg); err != nil {
			return fmt.Errorf("persist traffic switch config: %w", err)
		}
	}
	return nil
}

// Rollback reverts traffic back to the source server.
func (e *TrafficSwitchEngine) Rollback(ctx context.Context, migrationID int) error {
	cfg, err := e.repo.GetTrafficSwitchConfig(migrationID)
	if err != nil {
		return fmt.Errorf("get traffic switch config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no traffic switch config found for migration %d", migrationID)
	}

	switch cfg.Provider {
	case TrafficProviderCloudflare:
		return e.rollbackCloudflare(ctx, cfg)
	case TrafficProviderNginx:
		return e.rollbackNginx(ctx, cfg)
	case TrafficProviderTraefik:
		return e.rollbackTraefik(ctx, cfg)
	case TrafficProviderHAProxy:
		return e.rollbackHAProxy(ctx, cfg)
	case TrafficProviderCaddy:
		return e.rollbackCaddy(ctx, cfg)
	case TrafficProviderDocker:
		return e.rollbackDocker(ctx, cfg)
	case TrafficProviderDNS:
		return e.rollbackDNS(ctx, cfg)
	default:
		return fmt.Errorf("unsupported traffic provider: %s", cfg.Provider)
	}
}

// Verify checks that traffic is flowing to the target server.
func (e *TrafficSwitchEngine) Verify(ctx context.Context, cfg TrafficSwitchConfig) error {
	if cfg.HealthCheckURL == "" {
		return nil // no health check URL configured
	}

	req, err := http.NewRequestWithContext(ctx, "GET", cfg.HealthCheckURL, nil)
	if err != nil {
		return fmt.Errorf("create health check request: %w", err)
	}
	req.Header.Set("User-Agent", "Meshium-TrafficVerify/1.0")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("traffic verify health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	return fmt.Errorf("traffic verify failed: status code %d", resp.StatusCode)
}

// GetProviderConfig retrieves the current traffic configuration from the provider.
func (e *TrafficSwitchEngine) GetProviderConfig(ctx context.Context, provider TrafficProvider, migrationID int) (string, error) {
	switch provider {
	case TrafficProviderNginx:
		return e.getNginxConfig(ctx)
	case TrafficProviderCaddy:
		return e.getCaddyConfig(ctx)
	case TrafficProviderHAProxy:
		return e.getHAProxyConfig(ctx)
	case TrafficProviderTraefik:
		return e.getTraefikConfig(ctx)
	default:
		return "", nil
	}
}

// --- Cloudflare ---

type cloudflareDNSRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func (e *TrafficSwitchEngine) switchCloudflare(ctx context.Context, cfg *TrafficSwitchConfig) error {
	apiToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")
	if apiToken == "" || zoneID == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN and CLOUDFLARE_ZONE_ID must be set")
	}

	// Parse config for record details
	var record struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
	}
	if err := json.Unmarshal([]byte(cfg.NewConfig), &record); err != nil {
		return fmt.Errorf("parse cloudflare config: %w", err)
	}

	// Find existing record
	recordID, oldContent, err := e.findCloudflareRecord(ctx, apiToken, zoneID, record.Type, record.Name)
	if err != nil {
		return fmt.Errorf("find existing DNS record: %w", err)
	}

	// Store original for rollback
	cfg.RollbackConfig = fmt.Sprintf(`{"record_id":"%s","original_content":"%s"}`, recordID, oldContent)

	if recordID == "" {
		// Create new record
		return e.createCloudflareRecord(ctx, apiToken, zoneID, record.Type, record.Name, record.Content, record.Proxied)
	}

	// Update existing record
	return e.updateCloudflareRecord(ctx, apiToken, zoneID, recordID, record.Type, record.Name, record.Content, record.Proxied)
}

func (e *TrafficSwitchEngine) findCloudflareRecord(ctx context.Context, apiToken, zoneID, recordType, recordName string) (string, string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=%s&name=%s", zoneID, recordType, recordName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := e.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Result  []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	if !result.Success || len(result.Result) == 0 {
		return "", "", nil
	}
	return result.Result[0].ID, result.Result[0].Content, nil
}

func (e *TrafficSwitchEngine) updateCloudflareRecord(ctx context.Context, apiToken, zoneID, recordID, recordType, recordName, content string, proxied bool) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, recordID)
	body := cloudflareDNSRequest{Type: recordType, Name: recordName, Content: content, TTL: 1, Proxied: proxied}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("cloudflare API returned status %d", resp.StatusCode)
	}
	return nil
}

func (e *TrafficSwitchEngine) createCloudflareRecord(ctx context.Context, apiToken, zoneID, recordType, recordName, content string, proxied bool) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
	body := cloudflareDNSRequest{Type: recordType, Name: recordName, Content: content, TTL: 1, Proxied: proxied}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("cloudflare API returned status %d", resp.StatusCode)
	}
	return nil
}

func (e *TrafficSwitchEngine) rollbackCloudflare(ctx context.Context, cfg *TrafficSwitchConfig) error {
	var rollback struct {
		RecordID        string `json:"record_id"`
		OriginalContent string `json:"original_content"`
	}
	if err := json.Unmarshal([]byte(cfg.RollbackConfig), &rollback); err != nil {
		return fmt.Errorf("parse rollback config: %w", err)
	}

	if rollback.RecordID == "" {
		return nil
	}

	apiToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")

	// Parse original config for record type and name
	var record struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	json.Unmarshal([]byte(cfg.OriginalConfig), &record)
	if record.Type == "" {
		record.Type = "A"
	}

	return e.updateCloudflareRecord(ctx, apiToken, zoneID, rollback.RecordID, record.Type, record.Name, rollback.OriginalContent, false)
}

// --- Nginx ---

var upstreamRegex = regexp.MustCompile(`(?s)upstream\s+\w+\s*\{[^}]+\}`)

func (e *TrafficSwitchEngine) switchNginx(ctx context.Context, cfg *TrafficSwitchConfig) error {
	// Find nginx config files
	configPath := "/etc/nginx/nginx.conf"
	if cfg.NewConfig != "" {
		var nc struct {
			ConfigPath string `json:"configPath"`
			Upstream   string `json:"upstream"`
			NewServer  string `json:"newServer"`
		}
		if err := json.Unmarshal([]byte(cfg.NewConfig), &nc); err == nil && nc.ConfigPath != "" {
			configPath = nc.ConfigPath
		}
	}

	// Read current config
	currentConfig, _, _, err := e.targetSSH.ExecContext(ctx, fmt.Sprintf("cat %s", shared.ShellQuote(configPath)))
	if err != nil {
		return fmt.Errorf("read nginx config: %w", err)
	}
	cfg.OriginalConfig = currentConfig

	// Replace upstream server
	newConfig := currentConfig
	if matches := upstreamRegex.FindAllString(currentConfig, -1); len(matches) > 0 {
		for _, match := range matches {
			if strings.Contains(match, "server ") {
				// Replace the server line in the upstream block
				newUpstream := regexp.MustCompile(`server\s+[^\s;]+;`).ReplaceAllStringFunc(match, func(s string) string {
					return fmt.Sprintf("server %s;", e.extractTargetServer(cfg))
				})
				newConfig = strings.Replace(newConfig, match, newUpstream, 1)
			}
		}
	}

	// Upload new config
	if err := e.targetSSH.Upload(bytes.NewReader([]byte(newConfig)), configPath); err != nil {
		return fmt.Errorf("upload nginx config: %w", err)
	}

	// Test and reload nginx
	if _, _, _, err := e.targetSSH.ExecContext(ctx, "nginx -t 2>&1"); err != nil {
		// Revert on test failure
		e.targetSSH.Upload(bytes.NewReader([]byte(currentConfig)), configPath)
		return fmt.Errorf("nginx config test failed: %w", err)
	}

	if _, _, _, err := e.targetSSH.ExecContext(ctx, "nginx -s reload 2>&1"); err != nil {
		return fmt.Errorf("nginx reload failed: %w", err)
	}
	return nil
}

func (e *TrafficSwitchEngine) rollbackNginx(ctx context.Context, cfg *TrafficSwitchConfig) error {
	if cfg.OriginalConfig == "" {
		return nil
	}
	configPath := "/etc/nginx/nginx.conf"
	if cfg.NewConfig != "" {
		var nc struct {
			ConfigPath string `json:"configPath"`
		}
		if err := json.Unmarshal([]byte(cfg.NewConfig), &nc); err == nil && nc.ConfigPath != "" {
			configPath = nc.ConfigPath
		}
	}
	if err := e.targetSSH.Upload(bytes.NewReader([]byte(cfg.OriginalConfig)), configPath); err != nil {
		return fmt.Errorf("restore nginx config: %w", err)
	}
	e.targetSSH.ExecContext(ctx, "nginx -s reload 2>&1")
	return nil
}

func (e *TrafficSwitchEngine) getNginxConfig(ctx context.Context) (string, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "cat /etc/nginx/nginx.conf 2>/dev/null || cat /etc/nginx/conf.d/default.conf 2>/dev/null || echo ''")
	return output, err
}

// --- Caddy ---

func (e *TrafficSwitchEngine) switchCaddy(ctx context.Context, cfg *TrafficSwitchConfig) error {
	configPath := "/etc/caddy/Caddyfile"
	currentConfig, _, _, err := e.targetSSH.ExecContext(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shared.ShellQuote(configPath)))
	if err != nil {
		return fmt.Errorf("read Caddyfile: %w", err)
	}
	cfg.OriginalConfig = currentConfig

	// Update reverse_proxy directive
	newConfig := currentConfig
	targetServer := e.extractTargetServer(cfg)
	if targetServer != "" {
		newConfig = regexp.MustCompile(`reverse_proxy\s+[^\s{]+`).ReplaceAllString(newConfig, fmt.Sprintf("reverse_proxy %s", targetServer))
	}

	if err := e.targetSSH.Upload(bytes.NewReader([]byte(newConfig)), configPath); err != nil {
		return fmt.Errorf("upload Caddyfile: %w", err)
	}

	if _, _, _, err := e.targetSSH.ExecContext(ctx, "caddy validate --config /etc/caddy/Caddyfile 2>&1"); err != nil {
		e.targetSSH.Upload(bytes.NewReader([]byte(currentConfig)), configPath)
		return fmt.Errorf("caddy config validation failed: %w", err)
	}

	if _, _, _, err := e.targetSSH.ExecContext(ctx, "systemctl reload caddy 2>&1"); err != nil {
		return fmt.Errorf("caddy reload failed: %w", err)
	}
	return nil
}

func (e *TrafficSwitchEngine) rollbackCaddy(ctx context.Context, cfg *TrafficSwitchConfig) error {
	if cfg.OriginalConfig == "" {
		return nil
	}
	if err := e.targetSSH.Upload(bytes.NewReader([]byte(cfg.OriginalConfig)), "/etc/caddy/Caddyfile"); err != nil {
		return fmt.Errorf("restore Caddyfile: %w", err)
	}
	e.targetSSH.ExecContext(ctx, "systemctl reload caddy 2>&1")
	return nil
}

func (e *TrafficSwitchEngine) getCaddyConfig(ctx context.Context) (string, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "cat /etc/caddy/Caddyfile 2>/dev/null || echo ''")
	return output, err
}

// --- HAProxy ---

func (e *TrafficSwitchEngine) switchHAProxy(ctx context.Context, cfg *TrafficSwitchConfig) error {
	configPath := "/etc/haproxy/haproxy.cfg"
	currentConfig, _, _, err := e.targetSSH.ExecContext(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shared.ShellQuote(configPath)))
	if err != nil {
		return fmt.Errorf("read haproxy config: %w", err)
	}
	cfg.OriginalConfig = currentConfig

	// Update server lines in backend blocks
	newConfig := currentConfig
	targetServer := e.extractTargetServer(cfg)
	if targetServer != "" {
		newConfig = regexp.MustCompile(`server\s+\w+\s+[^\s:]+:\d+`).ReplaceAllString(newConfig, fmt.Sprintf("server target %s", targetServer))
	}

	if err := e.targetSSH.Upload(bytes.NewReader([]byte(newConfig)), configPath); err != nil {
		return fmt.Errorf("upload haproxy config: %w", err)
	}

	if _, _, _, err := e.targetSSH.ExecContext(ctx, "haproxy -c -f /etc/haproxy/haproxy.cfg 2>&1"); err != nil {
		e.targetSSH.Upload(bytes.NewReader([]byte(currentConfig)), configPath)
		return fmt.Errorf("haproxy config check failed: %w", err)
	}

	if _, _, _, err := e.targetSSH.ExecContext(ctx, "systemctl reload haproxy 2>&1"); err != nil {
		return fmt.Errorf("haproxy reload failed: %w", err)
	}
	return nil
}

func (e *TrafficSwitchEngine) rollbackHAProxy(ctx context.Context, cfg *TrafficSwitchConfig) error {
	if cfg.OriginalConfig == "" {
		return nil
	}
	if err := e.targetSSH.Upload(bytes.NewReader([]byte(cfg.OriginalConfig)), "/etc/haproxy/haproxy.cfg"); err != nil {
		return fmt.Errorf("restore haproxy config: %w", err)
	}
	e.targetSSH.ExecContext(ctx, "systemctl reload haproxy 2>&1")
	return nil
}

func (e *TrafficSwitchEngine) getHAProxyConfig(ctx context.Context) (string, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "cat /etc/haproxy/haproxy.cfg 2>/dev/null || echo ''")
	return output, err
}

// --- Traefik ---

func (e *TrafficSwitchEngine) switchTraefik(ctx context.Context, cfg *TrafficSwitchConfig) error {
	configPath := "/etc/traefik/dynamic.yml"
	if cfg.NewConfig != "" {
		var tc struct {
			DynamicPath string `json:"dynamicPath"`
		}
		if err := json.Unmarshal([]byte(cfg.NewConfig), &tc); err == nil && tc.DynamicPath != "" {
			configPath = tc.DynamicPath
		}
	}
	currentConfig, _, _, err := e.targetSSH.ExecContext(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shared.ShellQuote(configPath)))
	if err != nil {
		return fmt.Errorf("read traefik config: %w", err)
	}
	cfg.OriginalConfig = currentConfig

	// Update the dynamic config file
	if cfg.NewConfig != "" {
		if err := e.targetSSH.Upload(bytes.NewReader([]byte(cfg.NewConfig)), configPath); err != nil {
			return fmt.Errorf("upload traefik dynamic config: %w", err)
		}
	}

	// Traefik auto-reloads dynamic config
	return nil
}

func (e *TrafficSwitchEngine) rollbackTraefik(ctx context.Context, cfg *TrafficSwitchConfig) error {
	if cfg.OriginalConfig == "" {
		return nil
	}
	dynamicPath := "/etc/traefik/dynamic.yml"
	if cfg.NewConfig != "" {
		var tc struct {
			DynamicPath string `json:"dynamicPath"`
		}
		if err := json.Unmarshal([]byte(cfg.NewConfig), &tc); err == nil && tc.DynamicPath != "" {
			dynamicPath = tc.DynamicPath
		}
	}
	if err := e.targetSSH.Upload(bytes.NewReader([]byte(cfg.OriginalConfig)), dynamicPath); err != nil {
		return fmt.Errorf("restore traefik dynamic config: %w", err)
	}
	return nil
}

func (e *TrafficSwitchEngine) getTraefikConfig(ctx context.Context) (string, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "cat /etc/traefik/dynamic.yml 2>/dev/null || echo ''")
	return output, err
}

// --- Docker ---

func (e *TrafficSwitchEngine) switchDocker(ctx context.Context, cfg *TrafficSwitchConfig) error {
	// Update Docker Compose service ports/routes
	var dc struct {
		ComposePath string `json:"composePath"`
		ServiceName string `json:"serviceName"`
		NewPort     string `json:"newPort"`
	}
	if err := json.Unmarshal([]byte(cfg.NewConfig), &dc); err != nil {
		return fmt.Errorf("parse docker config: %w", err)
	}

	composePath := dc.ComposePath
	if composePath == "" {
		composePath = "docker-compose.yml"
	}

	currentConfig, _, _, err := e.targetSSH.ExecContext(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shared.ShellQuote(composePath)))
	if err != nil {
		return fmt.Errorf("read docker-compose.yml: %w", err)
	}
	cfg.OriginalConfig = currentConfig

	// Restart with new config
	if _, _, _, err := e.targetSSH.ExecContext(ctx, fmt.Sprintf("cd $(dirname %s) && docker compose up -d 2>&1", shared.ShellQuote(composePath))); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	return nil
}

func (e *TrafficSwitchEngine) rollbackDocker(ctx context.Context, cfg *TrafficSwitchConfig) error {
	if cfg.OriginalConfig == "" {
		return nil
	}
	// Restore and restart
	composePath := "docker-compose.yml"
	if cfg.NewConfig != "" {
		var dc struct {
			ComposePath string `json:"composePath"`
		}
		if err := json.Unmarshal([]byte(cfg.NewConfig), &dc); err == nil && dc.ComposePath != "" {
			composePath = dc.ComposePath
		}
	}
	if err := e.targetSSH.Upload(bytes.NewReader([]byte(cfg.OriginalConfig)), composePath); err != nil {
		return fmt.Errorf("restore docker-compose.yml: %w", err)
	}
	e.targetSSH.ExecContext(ctx, fmt.Sprintf("cd $(dirname %s) && docker compose up -d 2>&1", shared.ShellQuote(composePath)))
	return nil
}

// --- DNS ---

func (e *TrafficSwitchEngine) switchDNS(ctx context.Context, cfg *TrafficSwitchConfig) error {
	// For direct DNS updates, delegate to Cloudflare or generic DNS API
	// This is a simplified implementation
	return e.switchCloudflare(ctx, cfg)
}

func (e *TrafficSwitchEngine) rollbackDNS(ctx context.Context, cfg *TrafficSwitchConfig) error {
	return e.rollbackCloudflare(ctx, cfg)
}

// --- Helpers ---

func (e *TrafficSwitchEngine) extractTargetServer(cfg *TrafficSwitchConfig) string {
	var nc struct {
		NewServer string `json:"newServer"`
	}
	if json.Unmarshal([]byte(cfg.NewConfig), &nc) == nil && nc.NewServer != "" {
		return nc.NewServer
	}
	return ""
}
