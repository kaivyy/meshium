package discovery

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// DockerIntelligence holds deep Docker analysis results.
type DockerIntelligence struct {
	ComposeFiles    []ComposeFileAnalysis `json:"composeFiles,omitempty"`
	Dockerfiles     []DockerfileAnalysis  `json:"dockerfiles,omitempty"`
	RegistryAccess  []RegistryInfo        `json:"registryAccess,omitempty"`
	NetworkTopology []NetworkAnalysis     `json:"networkTopology,omitempty"`
	VolumeAnalysis  []VolumeAnalysis      `json:"volumeAnalysis,omitempty"`
}

// ComposeFileAnalysis holds parsed docker-compose file analysis.
type ComposeFileAnalysis struct {
	Path      string              `json:"path"`
	Services  []ComposeService    `json:"services,omitempty"`
	Networks  []string            `json:"networks,omitempty"`
	Volumes   []string            `json:"volumes,omitempty"`
	DependsOn map[string][]string `json:"dependsOn,omitempty"`
}

// ComposeService holds a single service from a compose file.
type ComposeService struct {
	Name          string   `json:"name"`
	Image         string   `json:"image,omitempty"`
	BuildContext  string   `json:"buildContext,omitempty"`
	Dockerfile    string   `json:"dockerfile,omitempty"`
	Ports         []string `json:"ports,omitempty"`
	Volumes       []string `json:"volumes,omitempty"`
	Networks      []string `json:"networks,omitempty"`
	DependsOn     []string `json:"dependsOn,omitempty"`
	RestartPolicy string   `json:"restartPolicy,omitempty"`
	Healthcheck   string   `json:"healthcheck,omitempty"`
	EnvVarNames   []string `json:"envVarNames,omitempty"`
	SecretNames   []string `json:"secretNames,omitempty"`
	ConfigNames   []string `json:"configNames,omitempty"`
}

// DockerfileAnalysis holds parsed Dockerfile analysis.
type DockerfileAnalysis struct {
	Path         string   `json:"path"`
	BaseImage    string   `json:"baseImage,omitempty"`
	MultiStage   bool     `json:"multiStage"`
	Stages       []string `json:"stages,omitempty"`
	ExposedPorts []int    `json:"exposedPorts,omitempty"`
	BuildArgs    []string `json:"buildArgs,omitempty"`
}

// RegistryInfo holds registry access information.
type RegistryInfo struct {
	URL       string `json:"url"`
	AuthType  string `json:"authType,omitempty"`
	Available bool   `json:"available"`
}

// NetworkAnalysis holds Docker network analysis.
type NetworkAnalysis struct {
	Name       string   `json:"name"`
	Driver     string   `json:"driver,omitempty"`
	Containers []string `json:"containers,omitempty"`
	Internal   bool     `json:"internal"`
}

// VolumeAnalysis holds Docker volume analysis.
type VolumeAnalysis struct {
	Name       string   `json:"name"`
	Driver     string   `json:"driver,omitempty"`
	Mountpoint string   `json:"mountpoint,omitempty"`
	Type       string   `json:"type"`
	UsedBy     []string `json:"usedBy,omitempty"`
	SizeMB     int64    `json:"sizeMb,omitempty"`
}

// AnalyzeDockerIntelligence performs deep analysis of Docker configuration.
func AnalyzeDockerIntelligence(ctx context.Context, client SSHExecuter, snapshot *ServerSnapshot) *DockerIntelligence {
	intel := &DockerIntelligence{}
	if client == nil {
		return intel
	}

	for _, path := range collectRemotePaths(ctx, client, `find / -name "docker-compose.yml" -o -name "compose.yaml" 2>/dev/null | head -20`) {
		content, err := execText(ctx, client, "cat "+path)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		analysis := parseComposeFileAnalysis(path, content)
		intel.ComposeFiles = append(intel.ComposeFiles, analysis)
	}

	for _, path := range collectRemotePaths(ctx, client, `find / -name "Dockerfile" 2>/dev/null | head -20`) {
		content, err := execText(ctx, client, "cat "+path)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		analysis := parseDockerfileAnalysis(path, content)
		intel.Dockerfiles = append(intel.Dockerfiles, analysis)
	}

	if out, err := execText(ctx, client, "docker info | grep Registry"); err == nil && strings.TrimSpace(out) != "" {
		intel.RegistryAccess = append(intel.RegistryAccess, parseRegistryInfo(ctx, client, out))
	}

	for _, name := range collectDockerListNames(ctx, client, "docker network ls") {
		if isDefaultDockerNetwork(name) {
			continue
		}
		analysis := NetworkAnalysis{Name: name}
		if out, err := execText(ctx, client, "docker network inspect "+name); err == nil && strings.TrimSpace(out) != "" {
			if parsed := parseDockerNetworkAnalysis(out, name); parsed != nil {
				analysis = *parsed
			}
		}
		intel.NetworkTopology = append(intel.NetworkTopology, analysis)
	}

	for _, name := range collectDockerListNames(ctx, client, "docker volume ls") {
		analysis := VolumeAnalysis{Name: name, Type: "volume"}
		if out, err := execText(ctx, client, "docker volume inspect "+name); err == nil && strings.TrimSpace(out) != "" {
			if parsed := parseDockerVolumeAnalysis(out, name); parsed != nil {
				analysis = *parsed
			}
		}
		intel.VolumeAnalysis = append(intel.VolumeAnalysis, analysis)
	}

	return intel
}

func collectRemotePaths(ctx context.Context, client SSHExecuter, cmd string) []string {
	out, err := execText(ctx, client, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return uniqueStrings(paths)
}

func collectDockerListNames(ctx context.Context, client SSHExecuter, cmd string) []string {
	out, err := execText(ctx, client, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "NETWORK ID") || strings.Contains(upper, "VOLUME NAME") || strings.HasPrefix(upper, "DRIVER ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case strings.Contains(cmd, "network"):
			if len(fields) == 1 {
				names = append(names, fields[0])
			} else {
				names = append(names, fields[len(fields)-1])
			}
			if isDefaultDockerNetwork(names[len(names)-1]) {
				names = names[:len(names)-1]
			}
		case strings.Contains(cmd, "volume"):
			if len(fields) == 1 {
				names = append(names, fields[0])
			} else {
				names = append(names, fields[len(fields)-1])
			}
		}
	}
	return uniqueStrings(names)
}

func isDefaultDockerNetwork(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "bridge", "host", "none":
		return true
	default:
		return false
	}
}

func parseComposeFileAnalysis(path, content string) ComposeFileAnalysis {
	analysis := ComposeFileAnalysis{Path: path, DependsOn: make(map[string][]string)}
	lines := strings.Split(content, "\n")

	section := ""
	var currentService *ComposeService
	activeBlock := ""
	activeBlockIndent := -1

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := indentationLevel(line)

		if indent == 0 {
			currentService = nil
			activeBlock = ""
			activeBlockIndent = -1
			switch trimmed {
			case "services:":
				section = "services"
			case "networks:":
				section = "networks"
			case "volumes:":
				section = "volumes"
			default:
				section = ""
			}
			continue
		}

		if section == "services" {
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				name := strings.TrimSuffix(trimmed, ":")
				currentService = &ComposeService{Name: name}
				analysis.Services = append(analysis.Services, *currentService)
				activeBlock = ""
				activeBlockIndent = -1
				continue
			}

			if currentService == nil {
				continue
			}

			if activeBlock != "" && indent <= activeBlockIndent {
				activeBlock = ""
				activeBlockIndent = -1
			}

			current := &analysis.Services[len(analysis.Services)-1]

			if indent == 4 {
				if strings.HasPrefix(trimmed, "image:") {
					current.Image = unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "image:")))
					continue
				}
				if strings.HasPrefix(trimmed, "restart:") {
					current.RestartPolicy = unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "restart:")))
					continue
				}
				if strings.HasPrefix(trimmed, "healthcheck:") {
					value := strings.TrimSpace(strings.TrimPrefix(trimmed, "healthcheck:"))
					if value != "" {
						current.Healthcheck = unquoteYAMLValue(value)
					} else {
						activeBlock = "healthcheck"
						activeBlockIndent = indent
					}
					continue
				}
				if strings.HasPrefix(trimmed, "build:") {
					value := strings.TrimSpace(strings.TrimPrefix(trimmed, "build:"))
					if value != "" {
						current.BuildContext = unquoteYAMLValue(value)
					} else {
						activeBlock = "build"
						activeBlockIndent = indent
					}
					continue
				}
				if strings.HasPrefix(trimmed, "ports:") {
					activeBlock = "ports"
					activeBlockIndent = indent
					continue
				}
				if strings.HasPrefix(trimmed, "volumes:") {
					activeBlock = "volumes"
					activeBlockIndent = indent
					continue
				}
				if strings.HasPrefix(trimmed, "networks:") {
					activeBlock = "networks"
					activeBlockIndent = indent
					continue
				}
				if strings.HasPrefix(trimmed, "depends_on:") {
					activeBlock = "depends_on"
					activeBlockIndent = indent
					continue
				}
				if strings.HasPrefix(trimmed, "environment:") {
					activeBlock = "environment"
					activeBlockIndent = indent
					continue
				}
				if strings.HasPrefix(trimmed, "secrets:") {
					activeBlock = "secrets"
					activeBlockIndent = indent
					continue
				}
				if strings.HasPrefix(trimmed, "configs:") {
					activeBlock = "configs"
					activeBlockIndent = indent
					continue
				}
			}

			if activeBlock != "" && indent > activeBlockIndent {
				sub := strings.TrimSpace(trimmed)
				switch activeBlock {
				case "build":
					if strings.HasPrefix(sub, "context:") {
						current.BuildContext = unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "context:")))
					} else if strings.HasPrefix(sub, "dockerfile:") {
						current.Dockerfile = unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "dockerfile:")))
					}
				case "ports":
					if strings.HasPrefix(sub, "-") {
						current.Ports = append(current.Ports, unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "-"))))
					}
				case "volumes":
					if strings.HasPrefix(sub, "-") {
						current.Volumes = append(current.Volumes, unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "-"))))
					}
				case "networks":
					if strings.HasPrefix(sub, "-") {
						current.Networks = append(current.Networks, unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "-"))))
					}
				case "depends_on":
					if strings.HasPrefix(sub, "-") {
						dep := unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "-")))
						if dep != "" {
							current.DependsOn = append(current.DependsOn, dep)
							analysis.DependsOn[current.Name] = append(analysis.DependsOn[current.Name], dep)
						}
					} else if strings.HasSuffix(sub, ":") {
						dep := strings.TrimSuffix(sub, ":")
						if dep != "" {
							current.DependsOn = append(current.DependsOn, dep)
							analysis.DependsOn[current.Name] = append(analysis.DependsOn[current.Name], dep)
						}
					}
				case "environment":
					if strings.HasPrefix(sub, "-") {
						if name := parseKeyName(strings.TrimSpace(strings.TrimPrefix(sub, "-"))); name != "" {
							current.EnvVarNames = append(current.EnvVarNames, name)
						}
					} else if key := parseKeyName(sub); key != "" {
						current.EnvVarNames = append(current.EnvVarNames, key)
					}
				case "secrets":
					if strings.HasPrefix(sub, "-") {
						if name := parseKeyName(strings.TrimSpace(strings.TrimPrefix(sub, "-"))); name != "" {
							current.SecretNames = append(current.SecretNames, name)
						}
					} else if key := parseKeyName(sub); key != "" {
						current.SecretNames = append(current.SecretNames, key)
					}
				case "configs":
					if strings.HasPrefix(sub, "-") {
						if name := parseKeyName(strings.TrimSpace(strings.TrimPrefix(sub, "-"))); name != "" {
							current.ConfigNames = append(current.ConfigNames, name)
						}
					} else if key := parseKeyName(sub); key != "" {
						current.ConfigNames = append(current.ConfigNames, key)
					}
				case "healthcheck":
					if strings.HasPrefix(sub, "test:") {
						current.Healthcheck = unquoteYAMLValue(strings.TrimSpace(strings.TrimPrefix(sub, "test:")))
					}
				}
			}
		}

		if section == "networks" && indent == 2 && strings.HasSuffix(trimmed, ":") {
			analysis.Networks = append(analysis.Networks, strings.TrimSuffix(trimmed, ":"))
		}
		if section == "volumes" && indent == 2 && strings.HasSuffix(trimmed, ":") {
			analysis.Volumes = append(analysis.Volumes, strings.TrimSuffix(trimmed, ":"))
		}
	}

	analysis.Services = normalizeComposeServices(analysis.Services)
	analysis.Networks = uniqueStrings(analysis.Networks)
	analysis.Volumes = uniqueStrings(analysis.Volumes)
	if len(analysis.DependsOn) == 0 {
		analysis.DependsOn = nil
	}
	return analysis
}

func normalizeComposeServices(services []ComposeService) []ComposeService {
	for i := range services {
		services[i].Ports = uniqueStrings(services[i].Ports)
		services[i].Volumes = uniqueStrings(services[i].Volumes)
		services[i].Networks = uniqueStrings(services[i].Networks)
		services[i].DependsOn = uniqueStrings(services[i].DependsOn)
		services[i].EnvVarNames = uniqueStrings(services[i].EnvVarNames)
		services[i].SecretNames = uniqueStrings(services[i].SecretNames)
		services[i].ConfigNames = uniqueStrings(services[i].ConfigNames)
	}
	return services
}

func parseDockerfileAnalysis(path, content string) DockerfileAnalysis {
	analysis := DockerfileAnalysis{Path: path}
	lines := strings.Split(content, "\n")
	stageNames := make([]string, 0, 2)
	var ports []int
	var buildArgs []string
	var firstImage string

	for _, rawLine := range lines {
		line := strings.TrimSpace(strings.TrimRight(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "FROM "):
			analysis.MultiStage = analysis.MultiStage || firstImage != ""
			image, alias := parseDockerFromLine(line)
			if firstImage == "" {
				firstImage = image
			}
			if alias != "" {
				stageNames = append(stageNames, alias)
			}
		case strings.HasPrefix(upper, "EXPOSE "):
			for _, token := range strings.Fields(strings.TrimSpace(line[6:])) {
				if port, ok := parseDockerPortToken(token); ok {
					ports = append(ports, port)
				}
			}
		case strings.HasPrefix(upper, "ARG "):
			arg := strings.TrimSpace(line[3:])
			if idx := strings.Index(arg, "="); idx >= 0 {
				arg = arg[:idx]
			}
			if arg != "" {
				buildArgs = append(buildArgs, strings.TrimSpace(arg))
			}
		}
	}

	analysis.BaseImage = firstImage
	analysis.Stages = uniqueStrings(stageNames)
	analysis.ExposedPorts = uniqueInts(ports)
	analysis.BuildArgs = uniqueStrings(buildArgs)
	return analysis
}

func parseDockerFromLine(line string) (image, alias string) {
	payload := strings.TrimSpace(line)
	payload = strings.TrimSpace(payload[4:])
	fields := strings.Fields(payload)
	if len(fields) == 0 {
		return "", ""
	}
	image = fields[0]
	for i := 1; i < len(fields); i++ {
		if strings.EqualFold(fields[i], "AS") && i+1 < len(fields) {
			alias = fields[i+1]
			break
		}
	}
	return image, alias
}

func parseDockerPortToken(token string) (int, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, false
	}
	if idx := strings.Index(token, "/"); idx >= 0 {
		token = token[:idx]
	}
	port, err := strconv.Atoi(token)
	if err != nil {
		return 0, false
	}
	return port, true
}

func parseRegistryInfo(ctx context.Context, client SSHExecuter, output string) RegistryInfo {
	info := RegistryInfo{Available: true, AuthType: "none"}
	if m := regexp.MustCompile(`https?://[^\s"']+`).FindString(output); m != "" {
		info.URL = strings.TrimRight(m, ",")
	} else {
		info.URL = strings.TrimSpace(output)
	}

	if dockerConfig, err := execText(ctx, client, "cat ~/.docker/config.json"); err == nil && dockerConfig != "" {
		lower := strings.ToLower(dockerConfig)
		switch {
		case strings.Contains(lower, "identitytoken"):
			info.AuthType = "token"
		case strings.Contains(lower, "auth"):
			info.AuthType = "login"
		}
	}

	return info
}

type dockerNetworkInspectJSON struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Internal   bool   `json:"Internal"`
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

func parseDockerNetworkAnalysis(output, fallbackName string) *NetworkAnalysis {
	var payload []dockerNetworkInspectJSON
	if err := json.Unmarshal([]byte(output), &payload); err != nil || len(payload) == 0 {
		return &NetworkAnalysis{Name: fallbackName}
	}
	first := payload[0]
	analysis := &NetworkAnalysis{Name: first.Name}
	if analysis.Name == "" {
		analysis.Name = fallbackName
	}
	analysis.Driver = first.Driver
	analysis.Internal = first.Internal
	for key, container := range first.Containers {
		name := container.Name
		if name == "" {
			name = key
		}
		analysis.Containers = append(analysis.Containers, name)
	}
	analysis.Containers = uniqueStrings(analysis.Containers)
	return analysis
}

type dockerVolumeInspectJSON struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
}

func parseDockerVolumeAnalysis(output, fallbackName string) *VolumeAnalysis {
	var payload []dockerVolumeInspectJSON
	if err := json.Unmarshal([]byte(output), &payload); err != nil || len(payload) == 0 {
		return &VolumeAnalysis{Name: fallbackName, Type: "volume"}
	}
	first := payload[0]
	analysis := &VolumeAnalysis{Name: first.Name, Driver: first.Driver, Mountpoint: first.Mountpoint, Type: "volume"}
	if analysis.Name == "" {
		analysis.Name = fallbackName
	}
	return analysis
}

func indentationLevel(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func parseKeyName(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, ":"); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
	line = strings.Trim(line, `"'`)
	if idx := strings.Index(line, "="); idx >= 0 {
		line = line[:idx]
	}
	return strings.TrimSpace(line)
}

func unquoteYAMLValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "-")
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}

func uniqueInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
