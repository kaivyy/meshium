package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Container recreation was `docker run -d --name X [-e…] [--label…] IMAGE`.
// Ports, mounts, networks and restart policy were all dropped, so a migrated
// container came up unreachable (no published ports), on the wrong network,
// with no data, and would not survive a reboot — while the step reported
// success.
func TestDockerCollectCapturesRuntimeShape(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("which docker", "/usr/bin/docker\n")
	ssh.addOutput("docker ps -a --format", "abc123def456|web|nginx:1.25|Up 2 hours|0.0.0.0:8080->80/tcp\n")
	ssh.addOutput("Config.Env", "abc123def456|FOO=1\n|||\n")
	ssh.addOutput("Config.Labels", "abc123def456|team=infra\n|||\n")
	ssh.addOutput("HostConfig.PortBindings", "abc123def456|8080:80/tcp\n|||\n")
	ssh.addOutput(".Mounts", "abc123def456|volume:appdata:/var/lib/app\n|||\n")
	ssh.addOutput("NetworkSettings.Networks", "abc123def456|frontend\n|||\n")
	ssh.addOutput("RestartPolicy", "abc123def456|unless-stopped\n|||\n")

	data, err := (&DockerCollector{}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var dd DockerData
	if err := json.Unmarshal(data.Data, &dd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dd.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(dd.Containers))
	}
	c := dd.Containers[0]

	if len(c.PortBindings) == 0 {
		t.Error("port bindings dropped — the migrated container would publish nothing")
	}
	if len(c.MountSpecs) == 0 {
		t.Error("mounts dropped — the migrated container would start with no data volume attached")
	}
	if len(c.Networks) == 0 {
		t.Error("networks dropped — the migrated container would land on the default bridge")
	}
	if c.RestartPolicy != "unless-stopped" {
		t.Errorf("restart policy = %q, want unless-stopped", c.RestartPolicy)
	}
}

// The recreate command must carry the captured shape.
func TestDockerRunCommandIncludesRuntimeShape(t *testing.T) {
	c := DockerContainer{
		Name:          "web",
		Image:         "nginx:1.25",
		Env:           map[string]string{"FOO": "1"},
		Labels:        map[string]string{"team": "infra"},
		PortBindings:  []string{"8080:80/tcp"},
		MountSpecs:    []string{"volume:appdata:/var/lib/app"},
		Networks:      []string{"frontend"},
		RestartPolicy: "unless-stopped",
	}
	cmd := buildDockerRunCommand(c)

	for _, want := range []string{
		"-p '8080:80/tcp'",
		"-v 'appdata:/var/lib/app'",
		"--network 'frontend'",
		"--restart 'unless-stopped'",
		"-e 'FOO'='1'",
		"--label 'team'='infra'",
		"'nginx:1.25'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("run command missing %s:\n  %s", want, cmd)
		}
	}
}

// A container whose data lives in a volume or bind mount CANNOT be migrated by
// this tool — volume contents are never copied. Starting it on the target
// anyway produces a container that looks healthy and is empty, which is worse
// than not starting it: the operator believes the data moved.
func TestContainersWithDataMountsAreFlaggedNotSilentlyStarted(t *testing.T) {
	withVolume := DockerContainer{Name: "db", Image: "postgres:16",
		MountSpecs: []string{"volume:pgdata:/var/lib/postgresql/data"}}
	withBind := DockerContainer{Name: "app", Image: "app:1",
		MountSpecs: []string{"bind:/srv/app/data:/data"}}
	stateless := DockerContainer{Name: "web", Image: "nginx:1.25"}
	tmpfsOnly := DockerContainer{Name: "cache", Image: "nginx:1.25",
		MountSpecs: []string{"tmpfs::/tmp/cache"}}

	if !ContainerCarriesData(withVolume) {
		t.Error("named-volume container not flagged as data-carrying")
	}
	if !ContainerCarriesData(withBind) {
		t.Error("bind-mount container not flagged as data-carrying")
	}
	if ContainerCarriesData(stateless) {
		t.Error("stateless container wrongly flagged")
	}
	if ContainerCarriesData(tmpfsOnly) {
		t.Error("tmpfs is ephemeral by definition and must not count as data")
	}
}

// Apply must surface the data gap per container and must not report a clean
// success when it started containers whose data did not come with them.
func TestDockerApplyWarnsOnDataCarryingContainers(t *testing.T) {
	dd := DockerData{
		Containers: []DockerContainer{
			{Name: "db", Image: "postgres:16", MountSpecs: []string{"volume:pgdata:/var/lib/postgresql/data"}},
		},
		Count: 1,
	}
	raw, _ := json.Marshal(dd)

	ssh := newMockSSH()
	ssh.addOutput("which docker", "/usr/bin/docker\n")

	var msgs []WSMessage
	err := (&DockerApplier{}).Apply(context.Background(), ssh, CategoryData{Type: "docker", Data: raw},
		func(m WSMessage) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var warned bool
	for _, m := range msgs {
		if m.Status == "warning" && strings.Contains(strings.ToLower(m.Value), "data") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning about un-migrated container data; messages=%v", msgs)
	}

	for _, cmd := range ssh.commands {
		if strings.Contains(cmd, "docker run") && strings.Contains(cmd, "postgres") {
			t.Errorf("started a data-carrying container whose data was never migrated: %q", cmd)
		}
	}
}
