package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// DockerDetailsCollector collects Docker engine details that are not part of
// the core DockerInfo snapshot.
type DockerDetailsCollector struct{}

func (c *DockerDetailsCollector) Name() string           { return "docker_details" }
func (c *DockerDetailsCollector) Timeout() time.Duration { return 20 * time.Second }

func (c *DockerDetailsCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	if out, _, exitCode, _ := exec.ExecContext(ctx, "which docker 2>/dev/null"); exitCode != 0 || strings.TrimSpace(out) == "" {
		return nil, nil
	}

	result := &dockerDetailsResult{}

	if out, err := execText(ctx, exec, `docker info -f '{{.DockerRootDir}}' 2>/dev/null`); err == nil {
		result.DockerRoot = strings.TrimSpace(out)
	} else {
		result.addError("docker_root", err)
	}

	if out, err := execText(ctx, exec, `docker info -f '{{.Driver}}' 2>/dev/null`); err == nil {
		result.StorageDriver = strings.TrimSpace(out)
	} else {
		result.addError("storage_driver", err)
	}

	if out, err := execText(ctx, exec, `containerd --version 2>/dev/null`); err == nil && out != "" {
		result.Containerd = &ContainerdInfo{Version: strings.TrimSpace(out), Running: true}
	}

	if out, err := execText(ctx, exec, `podman --version 2>/dev/null`); err == nil && out != "" {
		result.Podman = &PodmanInfo{Version: strings.TrimSpace(out), Containers: countPodmanContainers(ctx, exec)}
	}

	return result, nil
}

type dockerDetailsResult struct {
	DockerRoot    string
	StorageDriver string
	Containerd    *ContainerdInfo
	Podman        *PodmanInfo
	errors        []CollectorError
}

func (r *dockerDetailsResult) addError(collector string, err error) {
	if err == nil {
		return
	}
	r.errors = append(r.errors, CollectorError{Collector: collector, Error: err.Error()})
}

func (r *dockerDetailsResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func countPodmanContainers(ctx context.Context, exec transport.SSHExecuter) int {
	out, err := execText(ctx, exec, `podman ps -a --format '{{.Names}}' 2>/dev/null`)
	if err != nil || out == "" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// enrichDockerContainers adds detailed inspect-based metadata to containers.
func enrichDockerContainers(ctx context.Context, exec transport.SSHExecuter, containers []ContainerInfo) []ContainerInfo {
	for i := range containers {
		details, err := inspectDockerContainer(ctx, exec, containers[i].Name)
		if err != nil {
			continue
		}
		containers[i].RestartPolicy = details.RestartPolicy
		containers[i].Healthcheck = details.Healthcheck
		containers[i].Command = details.Command
		containers[i].Entrypoint = details.Entrypoint
		containers[i].EnvVarNames = details.EnvVarNames
		if details.ComposeService != "" {
			containers[i].ComposeService = details.ComposeService
		}
		if details.ImageDigest != "" {
			containers[i].ImageDigest = details.ImageDigest
		}
	}
	return containers
}

// enrichComposeProjects reads compose file contents and extracts basic project metadata.
func enrichComposeProjects(ctx context.Context, exec transport.SSHExecuter, projects []ComposeProject) []ComposeProject {
	for i := range projects {
		if projects[i].ConfigFiles == "" {
			continue
		}
		files := splitComposeFiles(projects[i].ConfigFiles)
		var contents []string
		projects[i].DependsOn = make(map[string][]string)
		for _, file := range files {
			out, err := execText(ctx, exec, fmt.Sprintf("cat %s 2>/dev/null", shellQuote(file)))
			if err != nil || out == "" {
				continue
			}
			contents = append(contents, out)
			parseComposeContent(out, &projects[i])
		}
		projects[i].Contents = strings.Join(contents, "\n\n")
		projects[i].Networks = uniqueStrings(projects[i].Networks)
		projects[i].Volumes = uniqueStrings(projects[i].Volumes)
	}
	return projects
}

// inspectDockerContainer returns detailed metadata from docker inspect.
type dockerInspectDetails struct {
	RestartPolicy  string
	Healthcheck    string
	Command        string
	Entrypoint     string
	EnvVarNames    []string
	ComposeService string
	ImageDigest    string
}

type dockerInspectJSON struct {
	Config struct {
		Cmd         []string          `json:"Cmd"`
		Entrypoint  []string          `json:"Entrypoint"`
		Env         []string          `json:"Env"`
		Labels      map[string]string `json:"Labels"`
		Healthcheck *struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	RepoDigests []string `json:"RepoDigests"`
}

func inspectDockerContainer(ctx context.Context, exec transport.SSHExecuter, name string) (*dockerInspectDetails, error) {
	out, err := execText(ctx, exec, fmt.Sprintf(`docker inspect %s 2>/dev/null`, shellQuote(name)))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, fmt.Errorf("empty docker inspect output for %s", name)
	}
	var payload []dockerInspectJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty docker inspect output for %s", name)
	}
	info := &dockerInspectDetails{}
	first := payload[0]
	info.RestartPolicy = first.HostConfig.RestartPolicy.Name
	if len(first.Config.Cmd) > 0 {
		info.Command = strings.Join(first.Config.Cmd, " ")
	}
	if len(first.Config.Entrypoint) > 0 {
		info.Entrypoint = strings.Join(first.Config.Entrypoint, " ")
	}
	if first.Config.Healthcheck != nil && len(first.Config.Healthcheck.Test) > 0 {
		info.Healthcheck = strings.Join(first.Config.Healthcheck.Test, " ")
	}
	for _, env := range first.Config.Env {
		if idx := strings.Index(env, "="); idx > 0 {
			info.EnvVarNames = append(info.EnvVarNames, env[:idx])
		}
	}
	info.EnvVarNames = uniqueStrings(info.EnvVarNames)
	if svc := first.Config.Labels["com.docker.compose.service"]; svc != "" {
		info.ComposeService = svc
	}
	if len(first.RepoDigests) > 0 {
		info.ImageDigest = first.RepoDigests[0]
	}
	return info, nil
}

func splitComposeFiles(files string) []string {
	var out []string
	for _, file := range strings.Split(files, ",") {
		file = strings.TrimSpace(file)
		if file != "" {
			out = append(out, file)
		}
	}
	return out
}

func parseComposeContent(content string, project *ComposeProject) {
	lines := strings.Split(content, "\n")
	inNetworks := false
	inVolumes := false
	inServices := false
	var currentService string
	var currentSection string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			currentService = ""
			currentSection = ""
			switch {
			case trimmed == "services:":
				inServices = true
				inNetworks = false
				inVolumes = false
			case trimmed == "networks:":
				inNetworks = true
				inVolumes = false
				inServices = false
			case trimmed == "volumes:":
				inVolumes = true
				inNetworks = false
				inServices = false
			default:
				inNetworks = false
				inVolumes = false
				inServices = false
			}
			continue
		}

		if inServices && indent == 2 && strings.HasSuffix(trimmed, ":") {
			currentService = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if inServices && currentService != "" && indent >= 4 {
			sub := strings.TrimSpace(trimmed)
			switch {
			case strings.HasPrefix(sub, "depends_on:"):
				currentSection = "depends_on"
			case strings.HasPrefix(sub, "network_mode:"):
				currentSection = ""
			case strings.HasPrefix(sub, "networks:"):
				currentSection = "networks"
			case strings.HasPrefix(sub, "volumes:"):
				currentSection = "volumes"
			default:
				if currentSection == "depends_on" {
					if strings.HasPrefix(sub, "-") {
						dep := strings.TrimSpace(strings.TrimPrefix(sub, "-"))
						if dep != "" {
							project.DependsOn[currentService] = append(project.DependsOn[currentService], dep)
						}
					} else if strings.Contains(sub, ":") {
						dep := strings.TrimSpace(strings.SplitN(sub, ":", 2)[0])
						if dep != "" {
							project.DependsOn[currentService] = append(project.DependsOn[currentService], dep)
						}
					}
				}
			}
		}

		if inNetworks && indent == 2 && strings.HasSuffix(trimmed, ":") {
			project.Networks = append(project.Networks, strings.TrimSuffix(trimmed, ":"))
		}
		if inVolumes && indent == 2 && strings.HasSuffix(trimmed, ":") {
			project.Volumes = append(project.Volumes, strings.TrimSuffix(trimmed, ":"))
		}
	}

	if len(project.DependsOn) == 0 {
		project.DependsOn = nil
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func sortComposeProjects(projects []ComposeProject) {
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
}

func parseComposeFileNames(label string) []string {
	parts := strings.Split(label, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return uniqueStrings(parts)
}

func parseDockerVersionField(v string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(v))
	return parsed
}
