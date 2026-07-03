package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// RuntimeCollector detects installed language/runtime versions.
type RuntimeCollector struct{}

func (c *RuntimeCollector) Name() string { return "runtime" }
func (c *RuntimeCollector) Timeout() time.Duration { return 20 * time.Second }

func (c *RuntimeCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &runtimeCollectorResult{}

	for _, runtime := range []struct {
		Name string
		Cmd  string
		Parse func(string) string
	}{
		{"node", "node --version 2>/dev/null", parseVersionPassthrough},
		{"python", "python3 --version 2>/dev/null || python --version 2>/dev/null", parseVersionPassthrough},
		{"go", "go version 2>/dev/null", parseGoVersion},
		{"java", "java -version 2>&1", parseJavaVersion},
		{"php", "php --version 2>/dev/null", parseVersionPassthrough},
		{"ruby", "ruby --version 2>/dev/null", parseVersionPassthrough},
		{"rust", "rustc --version 2>/dev/null", parseVersionPassthrough},
		{"dotnet", "dotnet --version 2>/dev/null", parseVersionPassthrough},
		{"bun", "bun --version 2>/dev/null", parseVersionPassthrough},
		{"deno", "deno --version 2>/dev/null", parseDenoVersion},
	} {
		if out, err := execText(ctx, exec, fmt.Sprintf("which %s >/dev/null 2>&1 && (%s)", runtime.Name, runtime.Cmd)); err == nil && out != "" {
			result.Runtimes = append(result.Runtimes, RuntimeInfo{Name: runtime.Name, Version: runtime.Parse(out), Path: runtime.Name})
		}
	}

	return result, nil
}

type runtimeCollectorResult struct {
	Runtimes []RuntimeInfo
	errors   []CollectorError
}

func (r *runtimeCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func parseVersionPassthrough(out string) string {
	return strings.TrimSpace(out)
}

func parseGoVersion(out string) string {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) >= 3 {
		return fields[2]
	}
	return strings.TrimSpace(out)
}

func parseJavaVersion(out string) string {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "version") {
			parts := strings.Split(line, "\"")
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return strings.TrimSpace(out)
}

func parseDenoVersion(out string) string {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "deno ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "deno "))
		}
	}
	return strings.TrimSpace(out)
}
