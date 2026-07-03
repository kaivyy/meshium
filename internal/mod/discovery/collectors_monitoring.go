package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// MonitoringCollector detects reverse proxies, queues, monitoring stacks, and process managers.
type MonitoringCollector struct{}

func (c *MonitoringCollector) Name() string           { return "monitoring" }
func (c *MonitoringCollector) Timeout() time.Duration { return 20 * time.Second }

func (c *MonitoringCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &monitoringCollectorResult{}
	ports, _ := collectListeningPorts(ctx, exec)

	result.ReverseProxies = append(result.ReverseProxies, detectReverseProxies(ctx, exec, ports)...)
	result.MessageQueues = append(result.MessageQueues, detectMessageQueues(ctx, exec, ports)...)
	result.Monitoring = append(result.Monitoring, detectMonitoringServices(ctx, exec, ports)...)
	result.ProcessManagers = append(result.ProcessManagers, detectProcessManagers(ctx, exec)...)
	return result, nil
}

type monitoringCollectorResult struct {
	ReverseProxies  []ReverseProxyInfo
	MessageQueues   []MessageQueueInfo
	Monitoring      []MonitoringServiceInfo
	ProcessManagers []ProcessManagerInfo
	errors          []CollectorError
}

func (r *monitoringCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func collectListeningPorts(ctx context.Context, exec transport.SSHExecuter) (map[int]bool, error) {
	out, err := execText(ctx, exec, `ss -tln 2>/dev/null`)
	if err != nil {
		return nil, err
	}
	ports := make(map[int]bool)
	for _, token := range strings.Fields(out) {
		if idx := strings.LastIndex(token, ":"); idx >= 0 && idx < len(token)-1 {
			if port := token[idx+1:]; isDigits(port) {
				ports[atoi(port)] = true
			}
		}
	}
	return ports, nil
}

func detectReverseProxies(ctx context.Context, exec transport.SSHExecuter, ports map[int]bool) []ReverseProxyInfo {
	var out []ReverseProxyInfo
	for _, item := range []struct {
		Type   string
		Port   int
		Cmd    string
		Config string
	}{
		{"nginx", 80, `nginx -v 2>&1`, "/etc/nginx/nginx.conf"},
		{"apache", 80, `apache2 -v 2>&1 || httpd -v 2>&1`, "/etc/apache2/apache2.conf"},
		{"caddy", 2019, `caddy version 2>/dev/null`, "/etc/caddy/Caddyfile"},
		{"traefik", 8080, `traefik version 2>/dev/null`, ""},
		{"haproxy", 80, `haproxy -v 2>&1`, "/etc/haproxy/haproxy.cfg"},
	} {
		version, running := detectVersionAndRunning(ctx, exec, item.Cmd, ports[item.Port])
		if running || version != "" {
			out = append(out, ReverseProxyInfo{Type: item.Type, Version: version, ConfigPath: item.Config, Active: running})
		}
	}
	return out
}

func detectMessageQueues(ctx context.Context, exec transport.SSHExecuter, ports map[int]bool) []MessageQueueInfo {
	var out []MessageQueueInfo
	for _, item := range []struct {
		Type  string
		Ports []int
		Cmd   string
	}{
		{"rabbitmq", []int{5672, 15672}, `rabbitmqctl status 2>/dev/null`},
		{"kafka", []int{9092}, `kafka-topics.sh --version 2>/dev/null || kafka-server-start.sh --version 2>/dev/null`},
		{"nats", []int{4222}, `nats-server --version 2>/dev/null`},
		{"activemq", []int{61616, 8161}, `activemq --version 2>/dev/null`},
	} {
		portOpen := false
		for _, p := range item.Ports {
			if ports[p] {
				portOpen = true
				break
			}
		}
		version, running := detectVersionAndRunning(ctx, exec, item.Cmd, portOpen)
		if running || version != "" {
			out = append(out, MessageQueueInfo{Type: item.Type, Version: version, Running: running, Port: firstPort(item.Ports)})
		}
	}
	return out
}

func detectMonitoringServices(ctx context.Context, exec transport.SSHExecuter, ports map[int]bool) []MonitoringServiceInfo {
	var out []MonitoringServiceInfo
	for _, item := range []struct {
		Type string
		Port int
		Cmd  string
	}{
		{"prometheus", 9090, `prometheus --version 2>/dev/null`},
		{"grafana", 3000, `grafana-server -v 2>/dev/null`},
		{"loki", 3100, `loki --version 2>/dev/null`},
		{"tempo", 3200, `tempo --version 2>/dev/null`},
		{"jaeger", 16686, `jaeger-query --version 2>/dev/null`},
	} {
		version, running := detectVersionAndRunning(ctx, exec, item.Cmd, ports[item.Port])
		if running || version != "" {
			out = append(out, MonitoringServiceInfo{Type: item.Type, Version: version, Port: item.Port, Running: running})
		}
	}
	return out
}

func detectProcessManagers(ctx context.Context, exec transport.SSHExecuter) []ProcessManagerInfo {
	var out []ProcessManagerInfo
	for _, item := range []struct {
		Type    string
		Cmd     string
		AppsCmd string
	}{
		{"pm2", `pm2 -v 2>/dev/null`, `pm2 jlist 2>/dev/null`},
		{"supervisor", `supervisord -v 2>/dev/null || supervisorctl version 2>/dev/null`, `supervisorctl status 2>/dev/null`},
	} {
		version, running := detectVersionAndRunning(ctx, exec, item.Cmd, false)
		if !running && version == "" {
			continue
		}
		apps := []string{}
		if outText, err := execText(ctx, exec, item.AppsCmd); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(outText), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				apps = append(apps, strings.Fields(line)[0])
			}
		}
		out = append(out, ProcessManagerInfo{Type: item.Type, Version: version, Apps: uniqueStrings(apps), Running: running})
	}
	return out
}

func detectVersionAndRunning(ctx context.Context, exec transport.SSHExecuter, cmd string, portOpen bool) (string, bool) {
	if cmd == "" {
		return "", portOpen
	}
	out, err := execText(ctx, exec, fmt.Sprintf("which %s >/dev/null 2>&1 && (%s)", strings.Fields(cmd)[0], cmd))
	if err != nil {
		return "", portOpen
	}
	return strings.TrimSpace(out), portOpen || strings.TrimSpace(out) != ""
}

func firstPort(ports []int) int {
	if len(ports) == 0 {
		return 0
	}
	return ports[0]
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	var v int
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%d", &v)
	return v
}
