package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"meshium/internal/mod/server"

	xssh "golang.org/x/crypto/ssh"
)

// DiffResult holds the comparison between source and target servers.
type DiffResult struct {
	SourceID  int              `json:"sourceId"`
	TargetID  int              `json:"targetId"`
	Categories []DiffCategory  `json:"categories"`
}

type DiffCategory struct {
	Category string       `json:"category"`
	OnlyInSource []string `json:"onlyInSource"`
	OnlyInTarget []string `json:"onlyInTarget"`
	Different    []string `json:"different"`
	Same         int      `json:"same"`
}

// DiffService compares source and target servers.
type DiffService struct {
	registry *CategoryRegistry
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

// NewDiffService creates a DiffService.
func NewDiffService(
	registry *CategoryRegistry,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
) *DiffService {
	return &DiffService{
		registry: registry,
		srvRepo:  srvRepo,
		pool:     pool,
		authSvc:  authSvc,
		hosts:    hosts,
	}
}

// Diff compares source and target servers for the given categories.
func (d *DiffService) Diff(ctx context.Context, sourceID, targetID int, categories []string, onProgress StepCallback) (*DiffResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// Get SSH connections to both servers
	sourceServer, err := d.srvRepo.GetByID(sourceID)
	if err != nil {
		return nil, fmt.Errorf("source server not found: %w", err)
	}
	targetServer, err := d.srvRepo.GetByID(targetID)
	if err != nil {
		return nil, fmt.Errorf("target server not found: %w", err)
	}

	onProgress(WSMessage{Step: "diff", Status: "progress", Value: "Connecting to source and target..."})

	sourceSSH, err := d.getSSHClient(sourceID, sourceServer)
	if err != nil {
		return nil, fmt.Errorf("source SSH connection failed: %w", err)
	}
	targetSSH, err := d.getSSHClient(targetID, targetServer)
	if err != nil {
		return nil, fmt.Errorf("target SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "diff", Status: "success", Value: "Connected to both servers"})

	result := &DiffResult{
		SourceID:  sourceID,
		TargetID:  targetID,
	}

	// If no categories specified, use all
	if len(categories) == 0 {
		categories = d.registry.Available()
	}

	for _, catName := range categories {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		mod, ok := d.registry.Get(catName)
		if !ok {
			continue
		}

		onProgress(WSMessage{
			Step:   "diff:" + catName,
			Status: "progress",
			Value:  "Comparing " + catName + "...",
		})

		// Collect from both source and target
		sourceData, err := mod.Collector.Collect(ctx, sourceSSH)
		if err != nil {
			continue
		}
		targetData, err := mod.Collector.Collect(ctx, targetSSH)
		if err != nil {
			continue
		}

		diff := d.computeDiff(catName, sourceData, targetData)
		result.Categories = append(result.Categories, diff)

		onProgress(WSMessage{
			Step:   "diff:" + catName,
			Status: "success",
			Value:  fmt.Sprintf("Compared %s: %d only in source, %d only in target, %d different", catName, len(diff.OnlyInSource), len(diff.OnlyInTarget), len(diff.Different)),
		})
	}

	onProgress(WSMessage{Step: "diff", Status: "complete", Value: "Diff complete"})
	return result, nil
}

func (d *DiffService) computeDiff(category string, source, target CategoryData) DiffCategory {
	switch category {
	case "packages":
		return d.diffPackages(source, target)
	case "configs":
		return d.diffConfigs(source, target)
	case "services":
		return d.diffServices(source, target)
	case "users":
		return d.diffUsers(source, target)
	case "docker":
		return d.diffDocker(source, target)
	default:
		return DiffCategory{Category: category}
	}
}

func (d *DiffService) diffPackages(source, target CategoryData) DiffCategory {
	var sd, td PackagesData
	json.Unmarshal(source.Data, &sd)
	json.Unmarshal(target.Data, &td)

	sourcePkgs := make(map[string]bool)
	for _, p := range sd.Packages {
		sourcePkgs[p] = true
	}
	targetPkgs := make(map[string]bool)
	for _, p := range td.Packages {
		targetPkgs[p] = true
	}

	var onlySource, onlyTarget []string
	same := 0
	for _, p := range sd.Packages {
		if targetPkgs[p] {
			same++
		} else {
			onlySource = append(onlySource, p)
		}
	}
	for _, p := range td.Packages {
		if !sourcePkgs[p] {
			onlyTarget = append(onlyTarget, p)
		}
	}

	sort.Strings(onlySource)
	sort.Strings(onlyTarget)

	return DiffCategory{
		Category:     "packages",
		OnlyInSource: onlySource,
		OnlyInTarget: onlyTarget,
		Same:         same,
	}
}

func (d *DiffService) diffConfigs(source, target CategoryData) DiffCategory {
	var sd, td ConfigsData
	json.Unmarshal(source.Data, &sd)
	json.Unmarshal(target.Data, &td)

	var onlySource, onlyTarget, different []string
	same := 0

	for path, srcContent := range sd.Files {
		tgtContent, exists := td.Files[path]
		if !exists {
			onlySource = append(onlySource, path)
		} else if !bytes.Equal(srcContent, tgtContent) {
			different = append(different, path)
		} else {
			same++
		}
	}

	for path := range td.Files {
		if _, exists := sd.Files[path]; !exists {
			onlyTarget = append(onlyTarget, path)
		}
	}

	sort.Strings(onlySource)
	sort.Strings(onlyTarget)
	sort.Strings(different)

	return DiffCategory{
		Category:     "configs",
		OnlyInSource: onlySource,
		OnlyInTarget: onlyTarget,
		Different:    different,
		Same:         same,
	}
}

func (d *DiffService) diffServices(source, target CategoryData) DiffCategory {
	var sd, td ServicesData
	json.Unmarshal(source.Data, &sd)
	json.Unmarshal(target.Data, &td)

	sourceSvc := make(map[string]bool)
	for _, s := range sd.Services {
		sourceSvc[s] = true
	}
	targetSvc := make(map[string]bool)
	for _, s := range td.Services {
		targetSvc[s] = true
	}

	var onlySource, onlyTarget []string
	same := 0
	for _, s := range sd.Services {
		if targetSvc[s] {
			same++
		} else {
			onlySource = append(onlySource, s)
		}
	}
	for _, s := range td.Services {
		if !sourceSvc[s] {
			onlyTarget = append(onlyTarget, s)
		}
	}

	sort.Strings(onlySource)
	sort.Strings(onlyTarget)

	return DiffCategory{
		Category:     "services",
		OnlyInSource: onlySource,
		OnlyInTarget: onlyTarget,
		Same:         same,
	}
}

func (d *DiffService) diffUsers(source, target CategoryData) DiffCategory {
	var sd, td UsersData
	json.Unmarshal(source.Data, &sd)
	json.Unmarshal(target.Data, &td)

	sourceUsers := make(map[string]bool)
	for _, u := range sd.Users {
		sourceUsers[u.Name] = true
	}
	targetUsers := make(map[string]bool)
	for _, u := range td.Users {
		targetUsers[u.Name] = true
	}

	var onlySource, onlyTarget []string
	same := 0
	for _, u := range sd.Users {
		if targetUsers[u.Name] {
			same++
		} else {
			onlySource = append(onlySource, u.Name)
		}
	}
	for _, u := range td.Users {
		if !sourceUsers[u.Name] {
			onlyTarget = append(onlyTarget, u.Name)
		}
	}

	sort.Strings(onlySource)
	sort.Strings(onlyTarget)

	return DiffCategory{
		Category:     "users",
		OnlyInSource: onlySource,
		OnlyInTarget: onlyTarget,
		Same:         same,
	}
}

func (d *DiffService) diffDocker(source, target CategoryData) DiffCategory {
	var sd, td DockerData
	json.Unmarshal(source.Data, &sd)
	json.Unmarshal(target.Data, &td)

	sourceContainers := make(map[string]bool)
	for _, c := range sd.Containers {
		sourceContainers[c.Name] = true
	}
	targetContainers := make(map[string]bool)
	for _, c := range td.Containers {
		targetContainers[c.Name] = true
	}

	var onlySource, onlyTarget []string
	same := 0
	for _, c := range sd.Containers {
		if targetContainers[c.Name] {
			same++
		} else {
			onlySource = append(onlySource, c.Name)
		}
	}
	for _, c := range td.Containers {
		if !sourceContainers[c.Name] {
			onlyTarget = append(onlyTarget, c.Name)
		}
	}

	// Also compare images
	sourceImages := make(map[string]bool)
	for _, img := range sd.Images {
		sourceImages[img] = true
	}
	for _, img := range td.Images {
		if !sourceImages[img] {
			onlyTarget = append(onlyTarget, "image:"+img)
		}
	}
	for _, img := range sd.Images {
		if _, exists := func() (bool, bool) {
			for _, timg := range td.Images {
				if timg == img {
					return true, true
				}
			}
			return false, false
		}(); !exists {
			onlySource = append(onlySource, "image:"+img)
		}
	}

	sort.Strings(onlySource)
	sort.Strings(onlyTarget)

	return DiffCategory{
		Category:     "docker",
		OnlyInSource: onlySource,
		OnlyInTarget: onlyTarget,
		Same:         same,
	}
}

// getSSHClient obtains an SSH connection for the given server.
func (d *DiffService) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	return getSSHClientForServer(serverID, srv, d.srvRepo, d.pool, d.authSvc, d.hosts)
}

var _ xssh.HostKeyCallback
