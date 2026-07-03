package discovery

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// SSLCertCollector detects certificate files and renewal information.
type SSLCertCollector struct{}

func (c *SSLCertCollector) Name() string { return "ssl" }
func (c *SSLCertCollector) Timeout() time.Duration { return 20 * time.Second }

func (c *SSLCertCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &sslCollectorResult{}

	if certs, err := collectSSLCerts(ctx, exec); err == nil {
		result.Certs = certs
	} else {
		result.addError("ssl", err)
	}

	return result, nil
}

type sslCollectorResult struct {
	Certs  []SSLCertInfo
	errors []CollectorError
}

func (r *sslCollectorResult) addError(collector string, err error) {
	if err == nil {
		return
	}
	r.errors = append(r.errors, CollectorError{Collector: collector, Error: err.Error()})
}

func (r *sslCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func collectSSLCerts(ctx context.Context, exec transport.SSHExecuter) ([]SSLCertInfo, error) {
	out, err := execText(ctx, exec, `find /etc /usr/local/etc -type f \( -name '*.crt' -o -name '*.pem' \) 2>/dev/null | head -100`)
	if err != nil || out == "" {
		return nil, err
	}
	var certs []SSLCertInfo
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cert, cerr := parseSSLCertFile(ctx, exec, path)
		if cerr != nil {
			continue
		}
		certs = append(certs, cert)
	}
	return uniqueSSLCerts(certs), nil
}

func parseSSLCertFile(ctx context.Context, exec transport.SSHExecuter, path string) (SSLCertInfo, error) {
	info := SSLCertInfo{Path: path, Domain: inferDomainFromPath(path)}
	out, err := execText(ctx, exec, fmt.Sprintf(`openssl x509 -in %s -noout -enddate -issuer 2>/dev/null`, shellQuote(path)))
	if err != nil || out == "" {
		return info, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "notAfter=") {
			info.Expiry = strings.TrimSpace(strings.TrimPrefix(line, "notAfter="))
		}
		if strings.HasPrefix(line, "issuer=") {
			info.Issuer = strings.TrimSpace(strings.TrimPrefix(line, "issuer="))
		}
	}
	if info.Expiry != "" {
		if t, perr := parseOpenSSLEndDate(info.Expiry); perr == nil {
			info.DaysRemaining = int(time.Until(t).Hours() / 24)
		}
	}
	info.AutoRenew = strings.Contains(path, "/letsencrypt/") || strings.Contains(path, "/certbot/")
	return info, nil
}

func inferDomainFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "_", ".")
	return base
}

func parseOpenSSLEndDate(value string) (time.Time, error) {
	return time.Parse("Jan _2 15:04:05 2006 MST", value)
}

func uniqueSSLCerts(certs []SSLCertInfo) []SSLCertInfo {
	seen := make(map[string]struct{}, len(certs))
	var out []SSLCertInfo
	for _, cert := range certs {
		key := cert.Path + "|" + cert.Domain
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cert)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

