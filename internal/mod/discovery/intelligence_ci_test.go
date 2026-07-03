package discovery

import (
	"context"
	"reflect"
	"testing"
)

func TestAnalyzeCIIntelligenceDetectsGitHubActions(t *testing.T) {
	ssh := newMockSSH()
	ssh.setResponse(`find / -path "*/.github/workflows/*.yml" 2>/dev/null | head -20`, "/srv/app/.github/workflows/build.yml\n/srv/app/.github/workflows/deploy.yml\n")
	ssh.setResponse("cat /srv/app/.github/workflows/build.yml", `name: build
on:
  push:
    branches:
      - main
      - release/*
jobs:
  build:
    runs-on: self-hosted
    steps:
      - uses: actions/checkout@v4
      - name: Build image
        run: docker build -t ghcr.io/acme/app:latest .
      - name: Push image
        run: docker push ghcr.io/acme/app:latest
        env:
          TOKEN: ${{ secrets.GHCR_TOKEN }}
`)
	ssh.setResponse("cat /srv/app/.github/workflows/deploy.yml", `name: deploy
on:
  workflow_dispatch:
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - run: echo deploy
`)
	ssh.setResponse(`find / -name ".gitlab-ci.yml" 2>/dev/null | head -10`, "")
	ssh.setResponse(`find / -name "Jenkinsfile" 2>/dev/null | head -10`, "")
	ssh.setResponse(`git -C /srv/app branch -a`, "* main\n  remotes/origin/main\n  remotes/origin/release/1.0\n")

	snapshot := &ServerSnapshot{
		GitRepos: []GitRepoInfo{{Path: "/srv/app"}},
	}
	intelligence := AnalyzeCIIntelligence(context.Background(), ssh, snapshot)
	if intelligence == nil || intelligence.GitHubActions == nil {
		t.Fatalf("expected github actions intelligence")
	}
	gh := intelligence.GitHubActions
	if !gh.SelfHostedRunner {
		t.Fatalf("expected self-hosted runner detection")
	}
	if !gh.DockerBuild || !gh.DockerPush {
		t.Fatalf("expected docker build/push detection: %#v", gh)
	}
	if gh.Registry != "ghcr" {
		t.Fatalf("expected ghcr registry, got %q", gh.Registry)
	}
	if !reflect.DeepEqual(gh.Branches, []string{"main", "release/1.0"}) {
		t.Fatalf("unexpected branches: %#v", gh.Branches)
	}
	if !reflect.DeepEqual(gh.SecretNames, []string{"GHCR_TOKEN"}) {
		t.Fatalf("expected secret names only, got %#v", gh.SecretNames)
	}
	if len(gh.Workflows) != 2 {
		t.Fatalf("expected two workflows, got %#v", gh.Workflows)
	}
	if len(gh.Workflows) > 0 && gh.Workflows[0].Path != "/srv/app/.github/workflows/build.yml" {
		t.Fatalf("unexpected first workflow: %#v", gh.Workflows[0])
	}
	if !containsString(gh.Workflows[0].Triggers, "push") {
		t.Fatalf("expected push trigger in first workflow: %#v", gh.Workflows[0])
	}
	if !gh.Workflows[0].HasDockerBuild || !gh.Workflows[0].HasDockerPush {
		t.Fatalf("expected workflow docker build/push flags: %#v", gh.Workflows[0])
	}
	if !containsString(gh.Workflows[0].ImageTags, "latest") {
		t.Fatalf("expected image tag capture: %#v", gh.Workflows[0].ImageTags)
	}
	if len(gh.Workflows[0].DeployEnvs) != 0 {
		t.Fatalf("unexpected deploy env capture: %#v", gh.Workflows[0].DeployEnvs)
	}
}

func TestAnalyzeCIIntelligenceDetectsGitLabAndJenkins(t *testing.T) {
	ssh := newMockSSH()
	ssh.setResponse(`find / -path "*/.github/workflows/*.yml" 2>/dev/null | head -20`, "")
	ssh.setResponse(`find / -name ".gitlab-ci.yml" 2>/dev/null | head -10`, "/srv/app/.gitlab-ci.yml\n")
	ssh.setResponse(`find / -name "Jenkinsfile" 2>/dev/null | head -10`, "/srv/app/Jenkinsfile\n")
	ssh.setResponse("cat /srv/app/.gitlab-ci.yml", `stages:
  - build
  - deploy
build:
  stage: build
  script:
    - docker build -t registry.example.com/app:1.0 .
  variables:
    SECRET_VALUE: hidden
`)
	ssh.setResponse("cat /srv/app/Jenkinsfile", `pipeline {
  agent any
  stages {
    stage('Build') {
      steps {
        sh 'docker build -t app:latest .'
      }
    }
  }
}
`)

	intelligence := AnalyzeCIIntelligence(context.Background(), ssh, &ServerSnapshot{GitRepos: []GitRepoInfo{{Path: "/srv/app"}}})
	if intelligence == nil {
		t.Fatalf("expected intelligence result")
	}
	if intelligence.GitLabCI == nil || intelligence.Jenkins == nil {
		t.Fatalf("expected gitlab and jenkins detection: %#v", intelligence)
	}
	if !intelligence.GitLabCI.HasDockerBuild {
		t.Fatalf("expected gitlab docker build detection")
	}
	if !reflect.DeepEqual(intelligence.GitLabCI.Stages, []string{"build", "deploy"}) {
		t.Fatalf("unexpected gitlab stages: %#v", intelligence.GitLabCI.Stages)
	}
	if !reflect.DeepEqual(intelligence.GitLabCI.SecretNames, []string{"SECRET_VALUE"}) {
		t.Fatalf("expected gitlab secret names only, got %#v", intelligence.GitLabCI.SecretNames)
	}
	if !intelligence.Jenkins.HasDocker {
		t.Fatalf("expected jenkins docker detection")
	}
}
