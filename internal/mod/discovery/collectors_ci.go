package discovery

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// CICollector detects CI/CD systems and workflow metadata.
type CICollector struct{}

func (c *CICollector) Name() string { return "ci" }
func (c *CICollector) Timeout() time.Duration { return 20 * time.Second }

func (c *CICollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &ciCollectorResult{}

	if info, err := collectGitHubActions(ctx, exec); err == nil && info != nil {
		result.CI = info
	} else if err != nil {
		result.addError("github_actions", err)
	}

	if present, err := fileExists(ctx, exec, ".gitlab-ci.yml"); err == nil && present {
		if result.CI == nil {
			result.CI = &CIInfo{}
		}
		if result.CI.Type == "" {
			result.CI.Type = "gitlab_ci"
		}
	} else if err != nil {
		result.addError("gitlab_ci", err)
	}

	if present, err := fileExists(ctx, exec, "Jenkinsfile"); err == nil && present {
		if result.CI == nil {
			result.CI = &CIInfo{}
		}
		if result.CI.Type == "" {
			result.CI.Type = "jenkins"
		}
	} else if err != nil {
		result.addError("jenkins", err)
	}

	return result, nil
}

type ciCollectorResult struct {
	CI     *CIInfo
	errors []CollectorError
}

func (r *ciCollectorResult) addError(collector string, err error) {
	if err == nil {
		return
	}
	r.errors = append(r.errors, CollectorError{Collector: collector, Error: err.Error()})
}

func (r *ciCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func collectGitHubActions(ctx context.Context, exec transport.SSHExecuter) (*CIInfo, error) {
	out, err := execText(ctx, exec, `find .github/workflows -type f \( -name '*.yml' -o -name '*.yaml' \) 2>/dev/null`)
	if err != nil || out == "" {
		return nil, nil
	}
	info := &CIInfo{Type: "github_actions"}
	secretRegex := regexp.MustCompile(`(?i)secrets\.([A-Z0-9_]+)`)
	workflowNames := make(map[string]struct{})

	for _, file := range strings.Split(strings.TrimSpace(out), "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		content, cerr := execText(ctx, exec, fmt.Sprintf("cat %s 2>/dev/null", shellQuote(file)))
		if cerr != nil || content == "" {
			continue
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "name:") && len(trimmed) > len("name:") {
				workflowNames[strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))] = struct{}{}
			}
			if strings.Contains(strings.ToLower(trimmed), "self-hosted") {
				info.SelfHosted = true
			}
			if strings.Contains(strings.ToLower(trimmed), "docker build") {
				info.DockerBuild = true
			}
			if strings.Contains(strings.ToLower(trimmed), "docker push") || strings.Contains(strings.ToLower(trimmed), "push:") {
				info.DockerPush = true
			}
			if strings.Contains(trimmed, "ghcr.io") {
				info.Registry = "ghcr.io"
			}
			for _, match := range secretRegex.FindAllStringSubmatch(trimmed, -1) {
				if len(match) > 1 {
					info.SecretNames = append(info.SecretNames, match[1])
				}
			}
		}
	}

	for name := range workflowNames {
		info.Workflows = append(info.Workflows, name)
	}
	sort.Strings(info.Workflows)
	info.SecretNames = uniqueStrings(info.SecretNames)
	if info.Type == "" {
		info.Type = "github_actions"
	}
	return info, nil
}

func fileExists(ctx context.Context, exec transport.SSHExecuter, path string) (bool, error) {
	cmd := fmt.Sprintf(`test -e %s`, shellQuote(path))
	_, _, exitCode, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return false, err
	}
	return exitCode == 0, nil
}
