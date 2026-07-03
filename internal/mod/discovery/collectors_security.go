package discovery

import (
	"context"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// SecurityCollector collects firewall and MAC framework status.
type SecurityCollector struct{}

func (c *SecurityCollector) Name() string           { return "security" }
func (c *SecurityCollector) Timeout() time.Duration { return 20 * time.Second }

func (c *SecurityCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &securityCollectorResult{}

	if fw, err := collectFirewall(ctx, exec); err == nil {
		result.Firewall = fw
	} else {
		result.addError("firewall", err)
	}

	if selinux, err := collectSELinux(ctx, exec); err == nil {
		result.SELinux = selinux
	} else {
		result.addError("selinux", err)
	}

	if apparmor, err := collectAppArmor(ctx, exec); err == nil {
		result.AppArmor = apparmor
	} else {
		result.addError("apparmor", err)
	}

	return result, nil
}

type securityCollectorResult struct {
	Firewall *FirewallInfo
	SELinux  *SELinuxInfo
	AppArmor *AppArmorInfo
	errors   []CollectorError
}

func (r *securityCollectorResult) addError(collector string, err error) {
	if err == nil {
		return
	}
	r.errors = append(r.errors, CollectorError{Collector: collector, Error: err.Error()})
}

func (r *securityCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func collectFirewall(ctx context.Context, exec transport.SSHExecuter) (*FirewallInfo, error) {
	if out, err := execText(ctx, exec, `ufw status verbose 2>/dev/null`); err == nil && out != "" {
		return parseUFWFirewall(out), nil
	}

	if out, err := execText(ctx, exec, `firewall-cmd --state 2>/dev/null`); err == nil && strings.TrimSpace(out) == "running" {
		rules := []string{}
		if rich, rerr := execText(ctx, exec, `firewall-cmd --list-all 2>/dev/null`); rerr == nil {
			rules = append(rules, strings.Split(strings.ReplaceAll(rich, "\n", " | "), " | ")...)
		}
		return &FirewallInfo{Type: "firewalld", Active: true, Rules: uniqueStrings(rules)}, nil
	}

	if out, err := execText(ctx, exec, `iptables -S 2>/dev/null`); err == nil && out != "" {
		return parseIPTablesFirewall(out), nil
	}

	return &FirewallInfo{Type: "none", Active: false}, nil
}

func parseUFWFirewall(out string) *FirewallInfo {
	fw := &FirewallInfo{Type: "ufw"}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "status:") && strings.Contains(lower, "active") {
			fw.Active = true
		}
		if strings.HasPrefix(lower, "default:") {
			rest := strings.TrimSpace(line[len("Default:"):])
			parts := strings.Split(rest, ",")
			if len(parts) >= 2 {
				inFields := strings.Fields(strings.TrimSpace(parts[0]))
				outFields := strings.Fields(strings.TrimSpace(parts[1]))
				if len(inFields) > 0 {
					fw.DefaultIn = inFields[0]
				}
				if len(outFields) > 0 {
					fw.DefaultOut = outFields[0]
				}
			}
		}
		if strings.HasPrefix(lower, "allow") || strings.HasPrefix(lower, "deny") || strings.HasPrefix(lower, "reject") {
			fw.Rules = append(fw.Rules, line)
		}
	}
	if !fw.Active {
		fw.Active = strings.Contains(strings.ToLower(out), "status: active")
	}
	fw.Rules = uniqueStrings(fw.Rules)
	return fw
}

func parseIPTablesFirewall(out string) *FirewallInfo {
	fw := &FirewallInfo{Type: "iptables", Active: true}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fw.Rules = append(fw.Rules, line)
		if strings.HasPrefix(line, "-P INPUT") && len(strings.Fields(line)) >= 3 {
			fw.DefaultIn = strings.Fields(line)[2]
		}
		if strings.HasPrefix(line, "-P OUTPUT") && len(strings.Fields(line)) >= 3 {
			fw.DefaultOut = strings.Fields(line)[2]
		}
	}
	fw.Rules = uniqueStrings(fw.Rules)
	return fw
}

func collectSELinux(ctx context.Context, exec transport.SSHExecuter) (*SELinuxInfo, error) {
	out, err := execText(ctx, exec, `getenforce 2>/dev/null`)
	if err != nil || out == "" {
		return &SELinuxInfo{Enabled: false, Mode: "disabled"}, nil
	}
	mode := strings.ToLower(strings.TrimSpace(out))
	info := &SELinuxInfo{Enabled: mode != "disabled", Mode: mode}
	if status, serr := execText(ctx, exec, `sestatus 2>/dev/null`); serr == nil {
		for _, line := range strings.Split(status, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "loaded policy name:") {
				info.Policy = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			}
		}
	}
	return info, nil
}

func collectAppArmor(ctx context.Context, exec transport.SSHExecuter) (*AppArmorInfo, error) {
	out, err := execText(ctx, exec, `aa-status --enabled 2>/dev/null`)
	if err != nil || out == "" {
		return &AppArmorInfo{Enabled: false}, nil
	}
	info := &AppArmorInfo{Enabled: true}
	if status, serr := execText(ctx, exec, `aa-status 2>/dev/null`); serr == nil {
		for _, line := range strings.Split(status, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(strings.ToLower(line), "profiles are in enforce mode") || strings.Contains(strings.ToLower(line), "profiles are in complain mode") {
				continue
			}
			if strings.HasPrefix(line, "/") || strings.Contains(line, " ") {
				continue
			}
			if line != "" && !strings.HasPrefix(strings.ToLower(line), "apparmor") {
				info.Profiles = append(info.Profiles, line)
			}
		}
	}
	return info, nil
}
