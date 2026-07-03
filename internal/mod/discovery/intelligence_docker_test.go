package discovery

import (
	"context"
	"reflect"
	"testing"
)

func TestAnalyzeDockerIntelligenceParsesComposeAndDockerfile(t *testing.T) {
	ssh := newMockSSH()
	ssh.setResponse(`find / -name "docker-compose.yml" -o -name "compose.yaml" 2>/dev/null | head -20`, "/srv/app/docker-compose.yml\n")
	ssh.setResponse("cat /srv/app/docker-compose.yml", `services:
  web:
    image: ghcr.io/acme/web:1.0
    build:
      context: .
      dockerfile: Dockerfile.web
      args:
        APP_ENV: production
        SECRET_TOKEN: should-not-be-captured
    ports:
      - "8080:80"
    volumes:
      - data:/var/lib/app
      - ./config:/etc/app
    networks:
      - frontend
      - backend
    depends_on:
      db:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://user:pass@db/app
      REDIS_URL: redis://cache:6379
    secrets:
      - web_tls
    configs:
      - app_config
  db:
    image: mysql:8
    environment:
      MYSQL_ROOT_PASSWORD: supersecret
    networks:
      - backend
networks:
  frontend:
  backend:
volumes:
  data:
`)
	ssh.setResponse(`find / -name "Dockerfile" 2>/dev/null | head -20`, "/srv/app/Dockerfile\n")
	ssh.setResponse("cat /srv/app/Dockerfile", `FROM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
EXPOSE 8080 8443/tcp
RUN go build ./...
FROM alpine:3.20
COPY --from=builder /src/app /app
EXPOSE 9090
`)
	ssh.setResponse("docker info | grep Registry", "Registry Mirrors: https://registry-1.docker.io/\n")
	ssh.setResponse("docker network ls", "bridge\napp_net\n")
	ssh.setResponse("docker network inspect app_net", `[{"Name":"app_net","Driver":"bridge","Internal":false,"Containers":{"abc":{"Name":"web"},"def":{"Name":"db"}}}]`+"\n")
	ssh.setResponse("docker volume ls", "data\n")
	ssh.setResponse("docker volume inspect data", `[{"Name":"data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/data/_data"}]`+"\n")

	intelligence := AnalyzeDockerIntelligence(context.Background(), ssh, &ServerSnapshot{})
	if intelligence == nil {
		t.Fatalf("expected docker intelligence result")
	}
	if len(intelligence.ComposeFiles) != 1 {
		t.Fatalf("expected 1 compose file analysis, got %d", len(intelligence.ComposeFiles))
	}
	compose := intelligence.ComposeFiles[0]
	if compose.Path != "/srv/app/docker-compose.yml" {
		t.Fatalf("unexpected compose path: %s", compose.Path)
	}
	if len(compose.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(compose.Services))
	}
	web := compose.Services[0]
	if web.Name != "web" {
		t.Fatalf("expected first service to be web, got %s", web.Name)
	}
	if web.Image != "ghcr.io/acme/web:1.0" {
		t.Fatalf("unexpected web image: %s", web.Image)
	}
	if web.BuildContext != "." || web.Dockerfile != "Dockerfile.web" {
		t.Fatalf("unexpected build info: context=%q dockerfile=%q", web.BuildContext, web.Dockerfile)
	}
	if !reflect.DeepEqual(web.DependsOn, []string{"db"}) {
		t.Fatalf("unexpected depends_on: %#v", web.DependsOn)
	}
	if !reflect.DeepEqual(web.EnvVarNames, []string{"DATABASE_URL", "REDIS_URL"}) {
		t.Fatalf("expected env var names only, got %#v", web.EnvVarNames)
	}
	if !reflect.DeepEqual(web.SecretNames, []string{"web_tls"}) {
		t.Fatalf("expected secret names only, got %#v", web.SecretNames)
	}
	if !reflect.DeepEqual(web.ConfigNames, []string{"app_config"}) {
		t.Fatalf("expected config names only, got %#v", web.ConfigNames)
	}
	if len(compose.DependsOn) == 0 || !reflect.DeepEqual(compose.DependsOn["web"], []string{"db"}) {
		t.Fatalf("unexpected compose depends_on map: %#v", compose.DependsOn)
	}
	if !containsString(compose.Networks, "frontend") || !containsString(compose.Networks, "backend") {
		t.Fatalf("unexpected compose networks: %#v", compose.Networks)
	}
	if !containsString(compose.Volumes, "data") {
		t.Fatalf("unexpected compose volumes: %#v", compose.Volumes)
	}

	if len(intelligence.Dockerfiles) != 1 {
		t.Fatalf("expected 1 dockerfile analysis, got %d", len(intelligence.Dockerfiles))
	}
	dockerfile := intelligence.Dockerfiles[0]
	if dockerfile.BaseImage != "golang:1.24-alpine" {
		t.Fatalf("unexpected base image: %s", dockerfile.BaseImage)
	}
	if !dockerfile.MultiStage {
		t.Fatalf("expected multi-stage dockerfile")
	}
	if !containsString(dockerfile.Stages, "builder") {
		t.Fatalf("expected builder stage, got %#v", dockerfile.Stages)
	}
	for _, port := range []int{8080, 8443, 9090} {
		if !containsInt(dockerfile.ExposedPorts, port) {
			t.Fatalf("expected exposed port %d in %#v", port, dockerfile.ExposedPorts)
		}
	}
	if !reflect.DeepEqual(dockerfile.BuildArgs, []string{"TARGETOS", "TARGETARCH"}) {
		t.Fatalf("expected build arg names only, got %#v", dockerfile.BuildArgs)
	}

	if len(intelligence.RegistryAccess) != 1 || !intelligence.RegistryAccess[0].Available {
		t.Fatalf("expected registry access to be detected, got %#v", intelligence.RegistryAccess)
	}
	if len(intelligence.NetworkTopology) != 1 {
		t.Fatalf("expected 1 network analysis, got %d", len(intelligence.NetworkTopology))
	}
	if intelligence.NetworkTopology[0].Name != "app_net" || !containsString(intelligence.NetworkTopology[0].Containers, "web") {
		t.Fatalf("unexpected network topology: %#v", intelligence.NetworkTopology[0])
	}
	if len(intelligence.VolumeAnalysis) != 1 || intelligence.VolumeAnalysis[0].Name != "data" || intelligence.VolumeAnalysis[0].Type != "volume" {
		t.Fatalf("unexpected volume analysis: %#v", intelligence.VolumeAnalysis)
	}
}

func TestAnalyzeDockerIntelligenceDoesNotCaptureSecretValues(t *testing.T) {
	ssh := newMockSSH()
	ssh.setResponse(`find / -name "docker-compose.yml" -o -name "compose.yaml" 2>/dev/null | head -20`, "/srv/app/docker-compose.yml\n")
	ssh.setResponse("cat /srv/app/docker-compose.yml", `services:
  api:
    environment:
      API_TOKEN: should-not-be-captured
      SIMPLE_FLAG: "true"
    secrets:
      - api_token
`)
	ssh.setResponse(`find / -name "Dockerfile" 2>/dev/null | head -20`, "")
	ssh.setResponse("docker info | grep Registry", "")
	ssh.setResponse("docker network ls", "")
	ssh.setResponse("docker volume ls", "")

	intelligence := AnalyzeDockerIntelligence(context.Background(), ssh, &ServerSnapshot{})
	if intelligence == nil || len(intelligence.ComposeFiles) != 1 {
		t.Fatalf("expected compose intelligence")
	}
	service := intelligence.ComposeFiles[0].Services[0]
	if containsString(service.EnvVarNames, "should-not-be-captured") {
		t.Fatalf("captured a secret value in env var names: %#v", service.EnvVarNames)
	}
	if !reflect.DeepEqual(service.EnvVarNames, []string{"API_TOKEN", "SIMPLE_FLAG"}) {
		t.Fatalf("unexpected env var names: %#v", service.EnvVarNames)
	}
	if !reflect.DeepEqual(service.SecretNames, []string{"api_token"}) {
		t.Fatalf("unexpected secret names: %#v", service.SecretNames)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
