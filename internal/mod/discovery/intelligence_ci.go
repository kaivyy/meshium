package discovery

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// CIIntelligence holds CI/CD analysis results.
type CIIntelligence struct {
	GitHubActions *GitHubActionsInfo `json:"githubActions,omitempty"`
	GitLabCI      *GitLabCIInfo      `json:"gitLabCi,omitempty"`
	Jenkins       *JenkinsInfo       `json:"jenkins,omitempty"`
}

// GitHubActionsInfo holds GitHub Actions analysis.
type GitHubActionsInfo struct {
	Workflows        []WorkflowInfo `json:"workflows,omitempty"`
	SelfHostedRunner bool           `json:"selfHostedRunner"`
	DockerBuild      bool           `json:"dockerBuild"`
	DockerPush       bool           `json:"dockerPush"`
	Registry         string         `json:"registry,omitempty"`
	Branches         []string       `json:"branches,omitempty"`
	SecretNames      []string       `json:"secretNames,omitempty"`
}

// WorkflowInfo holds a single GitHub Actions workflow.
type WorkflowInfo struct {
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Triggers       []string `json:"triggers,omitempty"`
	HasDockerBuild bool     `json:"hasDockerBuild"`
	HasDockerPush  bool     `json:"hasDockerPush"`
	ImageTags      []string `json:"imageTags,omitempty"`
	DeployEnvs     []string `json:"deployEnvs,omitempty"`
}

// GitLabCIInfo holds GitLab CI analysis.
type GitLabCIInfo struct {
	Path           string   `json:"path"`
	Stages         []string `json:"stages,omitempty"`
	HasDockerBuild bool     `json:"hasDockerBuild"`
	SecretNames    []string `json:"secretNames,omitempty"`
}

// JenkinsInfo holds Jenkins analysis.
type JenkinsInfo struct {
	Path      string `json:"path"`
	HasDocker bool   `json:"hasDocker"`
}

// AnalyzeCIIntelligence performs deep analysis of CI/CD configuration.
func AnalyzeCIIntelligence(ctx context.Context, client SSHExecuter, snapshot *ServerSnapshot) *CIIntelligence {
	intel := &CIIntelligence{}
	if client == nil {
		return intel
	}

	if gh := analyzeGitHubActionsIntelligence(ctx, client); gh != nil {
		intel.GitHubActions = gh
	}
	if gl := analyzeGitLabCIIntelligence(ctx, client); gl != nil {
		intel.GitLabCI = gl
	}
	if j := analyzeJenkinsIntelligence(ctx, client); j != nil {
		intel.Jenkins = j
	}

	if intel.GitHubActions != nil && snapshot != nil {
		intel.GitHubActions.Branches = collectGitBranches(ctx, client, snapshot.GitRepos)
		intel.GitHubActions.Workflows = sortWorkflowInfos(intel.GitHubActions.Workflows)
	}

	return intel
}

func analyzeGitHubActionsIntelligence(ctx context.Context, client SSHExecuter) *GitHubActionsInfo {
	paths := collectRemotePaths(ctx, client, `find / -path "*/.github/workflows/*.yml" 2>/dev/null | head -20`)
	if len(paths) == 0 {
		return nil
	}

	info := &GitHubActionsInfo{}
	secretSeen := make(map[string]struct{})

	for _, path := range paths {
		content, err := execText(ctx, client, "cat "+path)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		workflow, secrets, meta := parseGitHubWorkflow(path, content)
		info.Workflows = append(info.Workflows, workflow)
		info.SelfHostedRunner = info.SelfHostedRunner || meta.selfHostedRunner
		info.DockerBuild = info.DockerBuild || meta.dockerBuild
		info.DockerPush = info.DockerPush || meta.dockerPush
		if info.Registry == "" {
			info.Registry = meta.registry
		}
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			if _, ok := secretSeen[secret]; ok {
				continue
			}
			secretSeen[secret] = struct{}{}
			info.SecretNames = append(info.SecretNames, secret)
		}
	}

	info.SecretNames = uniqueStrings(info.SecretNames)
	return info
}

type workflowScanMeta struct {
	selfHostedRunner bool
	dockerBuild      bool
	dockerPush       bool
	registry         string
}

func parseGitHubWorkflow(path, content string) (WorkflowInfo, []string, workflowScanMeta) {
	workflow := WorkflowInfo{Path: path}
	var secrets []string
	meta := workflowScanMeta{}

	lines := strings.Split(content, "\n")
	section := ""
	jobSection := false
	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := indentationLevel(line)
		lower := strings.ToLower(trimmed)

		if indent == 0 {
			section = ""
			jobSection = false
			if strings.HasPrefix(trimmed, "name:") {
				workflow.Name = unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")))
			}
			if strings.HasPrefix(trimmed, "on:") {
				section = "on"
				jobSection = false
				if idx := strings.Index(trimmed, "["); idx >= 0 && strings.Contains(trimmed, "]") {
					inside := trimmed[idx+1 : strings.LastIndex(trimmed, "]")]
					for _, trigger := range strings.Split(inside, ",") {
						trigger = strings.TrimSpace(strings.Trim(trigger, `"'`))
						if trigger != "" {
							workflow.Triggers = append(workflow.Triggers, trigger)
						}
					}
				}
			}
			if strings.HasPrefix(trimmed, "jobs:") {
				section = "jobs"
				jobSection = true
			}
			continue
		}

		switch section {
		case "on":
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				trigger := strings.TrimSuffix(trimmed, ":")
				if trigger != "branches" && trigger != "paths" && trigger != "tags" {
					workflow.Triggers = append(workflow.Triggers, trigger)
				}
			}
		case "jobs":
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				jobSection = true
			}
			if !jobSection {
				continue
			}
			if strings.Contains(lower, "self-hosted") {
				meta.selfHostedRunner = true
			}
			if strings.Contains(lower, "docker build") || strings.Contains(lower, "docker/build-push-action") {
				workflow.HasDockerBuild = true
				meta.dockerBuild = true
			}
			if strings.Contains(lower, "docker push") || strings.Contains(lower, "push: true") {
				workflow.HasDockerPush = true
				meta.dockerPush = true
			}
			if strings.Contains(trimmed, "ghcr.io") {
				meta.registry = "ghcr"
			}
			if strings.Contains(lower, "docker.io") || strings.Contains(lower, "hub.docker.com") || strings.Contains(lower, "dockerhub") {
				if meta.registry == "" {
					meta.registry = "dockerhub"
				}
			}
			if strings.Contains(lower, "amazonaws.com") && strings.Contains(lower, ".dkr.ecr.") {
				if meta.registry == "" {
					meta.registry = "ecr"
				}
			}
			if strings.HasPrefix(lower, "environment:") {
				value := unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "environment:")))
				if value != "" {
					workflow.DeployEnvs = append(workflow.DeployEnvs, value)
				}
			}
			if matches := githubImageTagRegex.FindAllStringSubmatch(trimmed, -1); len(matches) > 0 {
				for _, match := range matches {
					if len(match) > 1 {
						if tag := extractImageTag(match[1]); tag != "" {
							workflow.ImageTags = append(workflow.ImageTags, tag)
						}
					}
				}
			}
		}

		if secretMatches := githubSecretRegex.FindAllStringSubmatch(trimmed, -1); len(secretMatches) > 0 {
			for _, match := range secretMatches {
				if len(match) > 1 {
					secrets = append(secrets, match[1])
				}
			}
		}
	}

	workflow.Triggers = uniqueStrings(workflow.Triggers)
	workflow.ImageTags = uniqueStrings(workflow.ImageTags)
	workflow.DeployEnvs = uniqueStrings(workflow.DeployEnvs)
	return workflow, uniqueStrings(secrets), meta
}

var (
	githubSecretRegex   = regexp.MustCompile(`(?i)secrets\.([A-Z0-9_]+)`)
	githubImageTagRegex = regexp.MustCompile(`(?i)(?:-t|tags:)\s+([^\s]+)`)
)

func extractImageTag(imageRef string) string {
	imageRef = strings.TrimSpace(strings.Trim(imageRef, `"'`))
	if imageRef == "" {
		return ""
	}
	if idx := strings.LastIndex(imageRef, "@"); idx >= 0 {
		imageRef = imageRef[:idx]
	}
	if idx := strings.LastIndex(imageRef, ":"); idx >= 0 && idx > strings.LastIndex(imageRef, "/") {
		return imageRef[idx+1:]
	}
	return imageRef
}

func analyzeGitLabCIIntelligence(ctx context.Context, client SSHExecuter) *GitLabCIInfo {
	paths := collectRemotePaths(ctx, client, `find / -name ".gitlab-ci.yml" 2>/dev/null | head -10`)
	if len(paths) == 0 {
		return nil
	}
	content, err := execText(ctx, client, "cat "+paths[0])
	if err != nil || strings.TrimSpace(content) == "" {
		return nil
	}

	info := &GitLabCIInfo{Path: paths[0]}
	section := ""
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := indentationLevel(line)
		lower := strings.ToLower(trimmed)

		if indent == 0 {
			if strings.HasPrefix(trimmed, "stages:") {
				section = "stages"
				continue
			}
			if strings.Contains(lower, "docker build") {
				info.HasDockerBuild = true
			}
			section = ""
			continue
		}

		switch section {
		case "stages":
			if indent == 2 && strings.HasPrefix(trimmed, "-") {
				info.Stages = append(info.Stages, unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
			}
		case "":
			if strings.Contains(lower, "docker build") {
				info.HasDockerBuild = true
			}
			if strings.HasPrefix(trimmed, "variables:") {
				section = "variables"
			}
		case "variables":
			if indent >= 4 && strings.Contains(trimmed, ":") {
				if key := parseKeyName(trimmed); key != "" {
					info.SecretNames = append(info.SecretNames, key)
				}
			}
		}

		if strings.HasPrefix(trimmed, "variables:") {
			section = "variables"
		}
		if strings.Contains(lower, "docker build") {
			info.HasDockerBuild = true
		}
	}

	info.Stages = uniqueStrings(info.Stages)
	info.SecretNames = uniqueStrings(info.SecretNames)
	return info
}

func analyzeJenkinsIntelligence(ctx context.Context, client SSHExecuter) *JenkinsInfo {
	paths := collectRemotePaths(ctx, client, `find / -name "Jenkinsfile" 2>/dev/null | head -10`)
	if len(paths) == 0 {
		return nil
	}
	content, err := execText(ctx, client, "cat "+paths[0])
	if err != nil || strings.TrimSpace(content) == "" {
		return nil
	}
	info := &JenkinsInfo{Path: paths[0]}
	lower := strings.ToLower(content)
	info.HasDocker = strings.Contains(lower, "docker build") || strings.Contains(lower, "docker push") || strings.Contains(lower, "docker run")
	return info
}

func collectGitBranches(ctx context.Context, client SSHExecuter, repos []GitRepoInfo) []string {
	seen := make(map[string]struct{})
	branches := make([]string, 0, 8)
	for _, repo := range repos {
		path := strings.TrimSpace(repo.Path)
		if path == "" {
			continue
		}
		out, err := execText(ctx, client, "git -C "+path+" branch -a")
		if err != nil || strings.TrimSpace(out) == "" {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			branch := normalizeGitBranch(line)
			if branch == "" {
				continue
			}
			if _, ok := seen[branch]; ok {
				continue
			}
			seen[branch] = struct{}{}
			branches = append(branches, branch)
		}
	}
	return branches
}

func normalizeGitBranch(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	line = strings.TrimPrefix(line, "*")
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "remotes/")
	line = strings.TrimPrefix(line, "origin/")
	line = strings.TrimSpace(line)
	return line
}

func uniqueStringsOrdered(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// GitHubActionsInfo and GitLabCIInfo use ordered unique slices; sort helper is
// kept here for any future call sites that need stable alphabetical output.
func sortStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func sortWorkflowInfos(values []WorkflowInfo) []WorkflowInfo {
	out := append([]WorkflowInfo(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}
