package drift

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
)

const (
	severityInfo     = "info"
	severityWarning  = "warning"
	severityCritical = "critical"
)

// Service compares discovery snapshots and reports configuration drift.
type Service struct {
	snapshotStore discovery.SnapshotStore
	serverRepo    server.Repo
	pool          *ssh.Pool
	authSvc       transport.AESKeyProvider
	knownHosts    transport.HostKeyStore
}

// DriftReport summarizes the differences between two snapshots.
type DriftReport struct {
	ServerID     int           `json:"serverId"`
	ServerName   string        `json:"serverName"`
	SnapshotA    string        `json:"snapshotA"`
	SnapshotB    string        `json:"snapshotB"`
	Changes      []DriftChange `json:"changes"`
	TotalChanges int           `json:"totalChanges"`
	Summary      DriftSummary  `json:"summary"`
}

// DriftChange describes a single drift event.
type DriftChange struct {
	Category string `json:"category"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	OldValue string `json:"oldValue,omitempty"`
	NewValue string `json:"newValue,omitempty"`
	Severity string `json:"severity"`
}

// DriftSummary aggregates drift counts for a comparison.
type DriftSummary struct {
	SnapshotA       string `json:"snapshotA,omitempty"`
	SnapshotB       string `json:"snapshotB,omitempty"`
	ComparedAt      string `json:"comparedAt,omitempty"`
	TotalChanges    int    `json:"totalChanges,omitempty"`
	PackagesAdded   int    `json:"packagesAdded"`
	PackagesRemoved int    `json:"packagesRemoved"`
	ServicesChanged int    `json:"servicesChanged"`
	DockerChanged   int    `json:"dockerChanged"`
	UsersChanged    int    `json:"usersChanged"`
	NetworkChanged  int    `json:"networkChanged"`
	DiskChanged     int    `json:"diskChanged"`
}

type snapshotLister interface {
	ListSnapshots(serverID int) ([]discovery.SnapshotRecord, error)
}

type snapshotFetcher interface {
	GetSnapshotByID(snapshotID string) (*discovery.SnapshotRecord, error)
}

var categoryOrder = map[string]int{
	"packages": 0,
	"services": 1,
	"docker":   2,
	"users":    3,
	"network":  4,
	"disk":     5,
	"system":   6,
}

var changeTypeOrder = map[string]int{
	"added":    0,
	"removed":  1,
	"modified": 2,
}

// NewService creates a drift service.
func NewService(snapshotStore discovery.SnapshotStore, serverRepo server.Repo, pool *ssh.Pool, authSvc transport.AESKeyProvider, knownHosts transport.HostKeyStore) *Service {
	return &Service{
		snapshotStore: snapshotStore,
		serverRepo:    serverRepo,
		pool:          pool,
		authSvc:       authSvc,
		knownHosts:    knownHosts,
	}
}

// CheckDrift compares the latest snapshot with the previous snapshot.
func (s *Service) CheckDrift(ctx context.Context, serverID int) (*DriftReport, error) {
	records, err := s.listSnapshotRecords(serverID)
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("not enough snapshots for server %d", serverID)
	}

	before := records[len(records)-2]
	after := records[len(records)-1]
	serverName := s.serverDisplayName(ctx, serverID)
	return s.buildReport(serverID, serverName, &before, &after), nil
}

// GetDriftHistory compares consecutive snapshots and returns drift summaries.
func (s *Service) GetDriftHistory(ctx context.Context, serverID int, limit int) ([]DriftSummary, error) {
	records, err := s.listSnapshotRecords(serverID)
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return []DriftSummary{}, nil
	}

	if limit <= 0 {
		limit = 10
	}

	serverName := s.serverDisplayName(ctx, serverID)
	maxComparisons := len(records) - 1
	if limit < maxComparisons {
		maxComparisons = limit
	}

	summaries := make([]DriftSummary, 0, maxComparisons)
	for i := len(records) - 1; i > 0 && len(summaries) < maxComparisons; i-- {
		a := records[i-1]
		b := records[i]
		summary := s.buildSummary(&a, &b)
		summary.ComparedAt = timestampString(snapshotTime(&b))
		summary.TotalChanges = summary.PackagesAdded + summary.PackagesRemoved + summary.ServicesChanged + summary.DockerChanged + summary.UsersChanged + summary.NetworkChanged + summary.DiskChanged
		summary.SnapshotA = timestampString(snapshotTime(&a))
		summary.SnapshotB = timestampString(snapshotTime(&b))
		_ = serverName
		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// GetDriftDetail compares two specific snapshots for a server.
func (s *Service) GetDriftDetail(ctx context.Context, serverID int, snapshotAID, snapshotBID string) (*DriftReport, error) {
	a, err := s.findSnapshotRecord(serverID, snapshotAID)
	if err != nil {
		return nil, err
	}
	b, err := s.findSnapshotRecord(serverID, snapshotBID)
	if err != nil {
		return nil, err
	}

	serverName := s.serverDisplayName(ctx, serverID)
	return s.buildReport(serverID, serverName, a, b), nil
}

// CompareServers compares the latest snapshots of two servers.
func (s *Service) CompareServers(ctx context.Context, sourceID, targetID int) (*DriftReport, error) {
	sourceSnapshot, err := s.snapshotStore.LoadSnapshot(sourceID)
	if err != nil {
		return nil, fmt.Errorf("load latest snapshot for source server %d: %w", sourceID, err)
	}
	targetSnapshot, err := s.snapshotStore.LoadSnapshot(targetID)
	if err != nil {
		return nil, fmt.Errorf("load latest snapshot for target server %d: %w", targetID, err)
	}

	sourceName := s.serverDisplayName(ctx, sourceID)
	sourceRecord := &discovery.SnapshotRecord{ServerID: sourceID, CapturedAt: snapshotTimeFromSnapshot(sourceSnapshot), Snapshot: sourceSnapshot}
	targetRecord := &discovery.SnapshotRecord{ServerID: targetID, CapturedAt: snapshotTimeFromSnapshot(targetSnapshot), Snapshot: targetSnapshot}
	return s.buildReport(sourceID, sourceName, sourceRecord, targetRecord), nil
}

func (s *Service) buildReport(serverID int, serverName string, a, b *discovery.SnapshotRecord) *DriftReport {
	changes, summary := s.compareSnapshots(a.Snapshot, b.Snapshot)
	summary.SnapshotA = timestampString(snapshotTime(a))
	summary.SnapshotB = timestampString(snapshotTime(b))
	summary.ComparedAt = summary.SnapshotB
	summary.TotalChanges = len(changes)

	return &DriftReport{
		ServerID:     serverID,
		ServerName:   serverName,
		SnapshotA:    summary.SnapshotA,
		SnapshotB:    summary.SnapshotB,
		Changes:      changes,
		TotalChanges: len(changes),
		Summary:      summary,
	}
}

func (s *Service) buildSummary(a, b *discovery.SnapshotRecord) DriftSummary {
	_, summary := s.compareSnapshots(a.Snapshot, b.Snapshot)
	return summary
}

func (s *Service) compareSnapshots(oldSnap, newSnap *discovery.ServerSnapshot) ([]DriftChange, DriftSummary) {
	if oldSnap == nil || newSnap == nil {
		return []DriftChange{}, DriftSummary{}
	}

	changes := make([]DriftChange, 0, 32)
	summary := DriftSummary{}

	var added, removed int
	changes, added, removed = appendStringSetChanges(changes, "packages", oldSnap.Packages, newSnap.Packages, severityInfo, severityWarning)
	summary.PackagesAdded += added
	summary.PackagesRemoved += removed

	changes, added, removed = appendStringSetChanges(changes, "users", oldSnap.Users, newSnap.Users, severityInfo, severityWarning)
	summary.UsersChanged += added + removed

	serviceChanges := compareServices(oldSnap.Services, newSnap.Services)
	changes = append(changes, serviceChanges...)
	summary.ServicesChanged += len(serviceChanges)

	dockerChanges := compareDocker(oldSnap.Docker, newSnap.Docker)
	changes = append(changes, dockerChanges...)
	summary.DockerChanged += len(dockerChanges)

	networkChanges := compareNetwork(oldSnap.NetworkPorts, newSnap.NetworkPorts)
	changes = append(changes, networkChanges...)
	summary.NetworkChanged += len(networkChanges)

	diskChanges := compareDisk(oldSnap.DiskUsage, newSnap.DiskUsage)
	changes = append(changes, diskChanges...)
	summary.DiskChanged += len(diskChanges)

	changes = append(changes, compareSystem(oldSnap, newSnap)...)

	sortDriftChanges(changes)
	return changes, summary
}

func (s *Service) listSnapshotRecords(serverID int) ([]discovery.SnapshotRecord, error) {
	lister, ok := s.snapshotStore.(snapshotLister)
	if !ok {
		return nil, fmt.Errorf("snapshot store does not support listing snapshots")
	}

	records, err := lister.ListSnapshots(serverID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CapturedAt.Equal(records[j].CapturedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CapturedAt.Before(records[j].CapturedAt)
	})
	return records, nil
}

func (s *Service) findSnapshotRecord(serverID int, snapshotID string) (*discovery.SnapshotRecord, error) {
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshot id is required")
	}

	fetcher, ok := s.snapshotStore.(snapshotFetcher)
	if ok {
		record, err := fetcher.GetSnapshotByID(snapshotID)
		if err == nil {
			if record.ServerID != 0 && record.ServerID != serverID {
				return nil, fmt.Errorf("snapshot %s does not belong to server %d", snapshotID, serverID)
			}
			return record, nil
		}
	}

	records, err := s.listSnapshotRecords(serverID)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if strconv.Itoa(records[i].ID) == snapshotID {
			record := records[i]
			return &record, nil
		}
	}
	return nil, fmt.Errorf("snapshot %s not found for server %d", snapshotID, serverID)
}

func (s *Service) serverDisplayName(_ context.Context, serverID int) string {
	if s.serverRepo == nil {
		return fmt.Sprintf("Server %d", serverID)
	}
	srv, err := s.serverRepo.GetByID(serverID)
	if err != nil || srv == nil {
		return fmt.Sprintf("Server %d", serverID)
	}
	if srv.Name != "" {
		return srv.Name
	}
	return fmt.Sprintf("Server %d", serverID)
}

func (s *Service) loadSnapshotRecord(serverID int) (*discovery.SnapshotRecord, error) {
	if getter, ok := s.snapshotStore.(interface {
		GetLatestSnapshotRecord(int) (*discovery.SnapshotRecord, error)
	}); ok {
		return getter.GetLatestSnapshotRecord(serverID)
	}
	if lister, ok := s.snapshotStore.(snapshotLister); ok {
		records, err := lister.ListSnapshots(serverID)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return nil, fmt.Errorf("no snapshot found for server %d", serverID)
		}
		record := records[len(records)-1]
		return &record, nil
	}
	return nil, fmt.Errorf("snapshot store does not support latest snapshot lookup")
}

func (s *Service) buildSummaryFromRecords(a, b *discovery.SnapshotRecord) DriftSummary {
	summary := s.buildSummary(a, b)
	summary.SnapshotA = timestampString(snapshotTime(a))
	summary.SnapshotB = timestampString(snapshotTime(b))
	summary.ComparedAt = summary.SnapshotB
	summary.TotalChanges = summary.PackagesAdded + summary.PackagesRemoved + summary.ServicesChanged + summary.DockerChanged + summary.UsersChanged + summary.NetworkChanged + summary.DiskChanged
	return summary
}

func appendStringSetChanges(changes []DriftChange, category string, oldValues, newValues []string, addedSeverity, removedSeverity string) ([]DriftChange, int, int) {
	oldSet := uniqueSortedStrings(oldValues)
	newSet := uniqueSortedStrings(newValues)

	oldLookup := make(map[string]struct{}, len(oldSet))
	for _, value := range oldSet {
		oldLookup[value] = struct{}{}
	}
	newLookup := make(map[string]struct{}, len(newSet))
	for _, value := range newSet {
		newLookup[value] = struct{}{}
	}

	added := 0
	for _, value := range newSet {
		if _, ok := oldLookup[value]; ok {
			continue
		}
		changes = append(changes, DriftChange{
			Category: category,
			Type:     "added",
			Name:     value,
			NewValue: value,
			Severity: addedSeverity,
		})
		added++
	}

	removed := 0
	for _, value := range oldSet {
		if _, ok := newLookup[value]; ok {
			continue
		}
		changes = append(changes, DriftChange{
			Category: category,
			Type:     "removed",
			Name:     value,
			OldValue: value,
			Severity: removedSeverity,
		})
		removed++
	}

	return changes, added, removed
}

func compareServices(oldServices, newServices []discovery.SystemService) []DriftChange {
	oldMap := make(map[string]discovery.SystemService, len(oldServices))
	newMap := make(map[string]discovery.SystemService, len(newServices))
	for _, svc := range oldServices {
		oldMap[svc.Name] = svc
	}
	for _, svc := range newServices {
		newMap[svc.Name] = svc
	}

	keys := unionKeys(oldMap, newMap)
	changes := make([]DriftChange, 0, len(keys))
	for _, name := range keys {
		oldSvc, oldOK := oldMap[name]
		newSvc, newOK := newMap[name]
		switch {
		case !oldOK && newOK:
			changes = append(changes, DriftChange{
				Category: "services",
				Type:     "added",
				Name:     name,
				NewValue: formatService(newSvc),
				Severity: serviceSeverityForAddition(newSvc),
			})
		case oldOK && !newOK:
			changes = append(changes, DriftChange{
				Category: "services",
				Type:     "removed",
				Name:     name,
				OldValue: formatService(oldSvc),
				Severity: serviceSeverityForRemoval(oldSvc),
			})
		case oldOK && newOK && serviceChanged(oldSvc, newSvc):
			changes = append(changes, DriftChange{
				Category: "services",
				Type:     "modified",
				Name:     name,
				OldValue: formatService(oldSvc),
				NewValue: formatService(newSvc),
				Severity: serviceSeverityForModification(oldSvc, newSvc),
			})
		}
	}
	return changes
}

func compareDocker(oldDocker, newDocker *discovery.DockerInfo) []DriftChange {
	if oldDocker == nil && newDocker == nil {
		return nil
	}
	if oldDocker == nil {
		changes := make([]DriftChange, 0, len(newDocker.Containers))
		for _, container := range newDocker.Containers {
			changes = append(changes, DriftChange{
				Category: "docker",
				Type:     "added",
				Name:     container.Name,
				NewValue: formatContainer(container),
				Severity: containerSeverityForAddition(container),
			})
		}
		return changes
	}
	if newDocker == nil {
		changes := make([]DriftChange, 0, len(oldDocker.Containers))
		for _, container := range oldDocker.Containers {
			changes = append(changes, DriftChange{
				Category: "docker",
				Type:     "removed",
				Name:     container.Name,
				OldValue: formatContainer(container),
				Severity: containerSeverityForRemoval(container),
			})
		}
		return changes
	}

	oldMap := make(map[string]discovery.ContainerInfo, len(oldDocker.Containers))
	newMap := make(map[string]discovery.ContainerInfo, len(newDocker.Containers))
	for _, container := range oldDocker.Containers {
		oldMap[container.Name] = container
	}
	for _, container := range newDocker.Containers {
		newMap[container.Name] = container
	}

	keys := unionKeys(oldMap, newMap)
	changes := make([]DriftChange, 0, len(keys))
	for _, name := range keys {
		oldContainer, oldOK := oldMap[name]
		newContainer, newOK := newMap[name]
		switch {
		case !oldOK && newOK:
			changes = append(changes, DriftChange{
				Category: "docker",
				Type:     "added",
				Name:     name,
				NewValue: formatContainer(newContainer),
				Severity: containerSeverityForAddition(newContainer),
			})
		case oldOK && !newOK:
			changes = append(changes, DriftChange{
				Category: "docker",
				Type:     "removed",
				Name:     name,
				OldValue: formatContainer(oldContainer),
				Severity: containerSeverityForRemoval(oldContainer),
			})
		case oldOK && newOK && containerChanged(oldContainer, newContainer):
			changes = append(changes, DriftChange{
				Category: "docker",
				Type:     "modified",
				Name:     name,
				OldValue: formatContainer(oldContainer),
				NewValue: formatContainer(newContainer),
				Severity: containerSeverityForModification(oldContainer, newContainer),
			})
		}
	}
	return changes
}

func compareNetwork(oldPorts, newPorts []discovery.OpenPort) []DriftChange {
	oldMap := make(map[string]discovery.OpenPort, len(oldPorts))
	newMap := make(map[string]discovery.OpenPort, len(newPorts))
	for _, port := range oldPorts {
		oldMap[portKey(port)] = port
	}
	for _, port := range newPorts {
		newMap[portKey(port)] = port
	}

	keys := unionKeys(oldMap, newMap)
	changes := make([]DriftChange, 0, len(keys))
	for _, key := range keys {
		oldPort, oldOK := oldMap[key]
		newPort, newOK := newMap[key]
		switch {
		case !oldOK && newOK:
			changes = append(changes, DriftChange{
				Category: "network",
				Type:     "added",
				Name:     portDisplayName(newPort),
				NewValue: formatOpenPort(newPort),
				Severity: severityInfo,
			})
		case oldOK && !newOK:
			changes = append(changes, DriftChange{
				Category: "network",
				Type:     "removed",
				Name:     portDisplayName(oldPort),
				OldValue: formatOpenPort(oldPort),
				Severity: severityWarning,
			})
		case oldOK && newOK && portChanged(oldPort, newPort):
			changes = append(changes, DriftChange{
				Category: "network",
				Type:     "modified",
				Name:     portDisplayName(oldPort),
				OldValue: formatOpenPort(oldPort),
				NewValue: formatOpenPort(newPort),
				Severity: severityWarning,
			})
		}
	}
	return changes
}

func compareDisk(oldDisks, newDisks []discovery.DiskPartition) []DriftChange {
	oldMap := make(map[string]discovery.DiskPartition, len(oldDisks))
	newMap := make(map[string]discovery.DiskPartition, len(newDisks))
	for _, disk := range oldDisks {
		oldMap[disk.MountPoint] = disk
	}
	for _, disk := range newDisks {
		newMap[disk.MountPoint] = disk
	}

	keys := unionKeys(oldMap, newMap)
	changes := make([]DriftChange, 0, len(keys))
	for _, mount := range keys {
		oldDisk, oldOK := oldMap[mount]
		newDisk, newOK := newMap[mount]
		switch {
		case !oldOK && newOK:
			changes = append(changes, DriftChange{
				Category: "disk",
				Type:     "added",
				Name:     mount,
				NewValue: formatDisk(newDisk),
				Severity: severityInfo,
			})
		case oldOK && !newOK:
			changes = append(changes, DriftChange{
				Category: "disk",
				Type:     "removed",
				Name:     mount,
				OldValue: formatDisk(oldDisk),
				Severity: severityWarning,
			})
		case oldOK && newOK && diskChanged(oldDisk, newDisk):
			changes = append(changes, DriftChange{
				Category: "disk",
				Type:     "modified",
				Name:     mount,
				OldValue: formatDisk(oldDisk),
				NewValue: formatDisk(newDisk),
				Severity: diskSeverityForModification(oldDisk, newDisk),
			})
		}
	}
	return changes
}

func compareSystem(oldSnap, newSnap *discovery.ServerSnapshot) []DriftChange {
	changes := make([]DriftChange, 0, 16)

	appendSystemChange := func(name, oldValue, newValue, severity string) {
		if oldValue == newValue {
			return
		}
		changes = append(changes, DriftChange{
			Category: "system",
			Type:     "modified",
			Name:     name,
			OldValue: oldValue,
			NewValue: newValue,
			Severity: severity,
		})
	}

	appendSystemChange("hostname", oldSnap.OS.Hostname, newSnap.OS.Hostname, severityCritical)
	appendSystemChange("os.distro", oldSnap.OS.Distro, newSnap.OS.Distro, severityCritical)
	appendSystemChange("os.kernel", oldSnap.OS.Kernel, newSnap.OS.Kernel, severityCritical)
	appendSystemChange("os.architecture", oldSnap.OS.Architecture, newSnap.OS.Architecture, severityWarning)
	appendSystemChange("os.timezone", oldSnap.OS.Timezone, newSnap.OS.Timezone, severityInfo)
	appendSystemChange("os.virtualization", oldSnap.OS.Virtualization, newSnap.OS.Virtualization, severityWarning)
	appendSystemChange("os.uptimeSeconds", strconv.FormatInt(oldSnap.OS.Uptime, 10), strconv.FormatInt(newSnap.OS.Uptime, 10), severityInfo)

	appendSystemChange("hardware.cpuModel", oldSnap.Hardware.CPUModel, newSnap.Hardware.CPUModel, severityWarning)
	appendSystemChange("hardware.cpuCores", strconv.Itoa(oldSnap.Hardware.CPUCores), strconv.Itoa(newSnap.Hardware.CPUCores), severityWarning)
	appendSystemChange("hardware.ramTotalMb", strconv.Itoa(oldSnap.Hardware.RAMTotalMB), strconv.Itoa(newSnap.Hardware.RAMTotalMB), severityWarning)
	appendSystemChange("hardware.ramUsedMb", strconv.Itoa(oldSnap.Hardware.RAMUsedMB), strconv.Itoa(newSnap.Hardware.RAMUsedMB), severityWarning)
	appendSystemChange("hardware.diskTotalGb", formatFloat(oldSnap.Hardware.DiskTotalGB), formatFloat(newSnap.Hardware.DiskTotalGB), severityWarning)
	appendSystemChange("hardware.diskUsedGb", formatFloat(oldSnap.Hardware.DiskUsedGB), formatFloat(newSnap.Hardware.DiskUsedGB), severityWarning)

	return changes
}

func sortDriftChanges(changes []DriftChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		iCat := categoryOrder[changes[i].Category]
		jCat := categoryOrder[changes[j].Category]
		if iCat != jCat {
			return iCat < jCat
		}
		iType := changeTypeOrder[changes[i].Type]
		jType := changeTypeOrder[changes[j].Type]
		if iType != jType {
			return iType < jType
		}
		if changes[i].Name != changes[j].Name {
			return changes[i].Name < changes[j].Name
		}
		return changes[i].Severity < changes[j].Severity
	})
}

func unionKeys[T any](left, right map[string]T) []string {
	keys := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sameStringSliceIgnoreOrder(left, right []string) bool {
	leftSet := uniqueSortedStrings(left)
	rightSet := uniqueSortedStrings(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for i := range leftSet {
		if leftSet[i] != rightSet[i] {
			return false
		}
	}
	return true
}

func serviceChanged(oldSvc, newSvc discovery.SystemService) bool {
	return oldSvc.Description != newSvc.Description ||
		oldSvc.LoadState != newSvc.LoadState ||
		oldSvc.ActiveState != newSvc.ActiveState ||
		oldSvc.SubState != newSvc.SubState ||
		oldSvc.Type != newSvc.Type ||
		!sameStringSliceIgnoreOrder(oldSvc.DependsOn, newSvc.DependsOn)
}

func serviceSeverityForAddition(svc discovery.SystemService) string {
	if isActiveService(svc) {
		return severityInfo
	}
	return severityWarning
}

func serviceSeverityForRemoval(svc discovery.SystemService) string {
	if isActiveService(svc) {
		return severityCritical
	}
	return severityWarning
}

func serviceSeverityForModification(oldSvc, newSvc discovery.SystemService) string {
	if isActiveService(oldSvc) && !isActiveService(newSvc) {
		return severityCritical
	}
	if !isActiveService(oldSvc) && isActiveService(newSvc) {
		return severityInfo
	}
	if oldSvc.ActiveState != newSvc.ActiveState || oldSvc.SubState != newSvc.SubState {
		return severityWarning
	}
	return severityInfo
}

func isActiveService(svc discovery.SystemService) bool {
	return strings.EqualFold(svc.ActiveState, "active") || strings.EqualFold(svc.SubState, "running")
}

func formatService(svc discovery.SystemService) string {
	parts := []string{
		"active=" + emptyFallback(svc.ActiveState),
		"sub=" + emptyFallback(svc.SubState),
		"load=" + emptyFallback(svc.LoadState),
		"type=" + emptyFallback(svc.Type),
	}
	if svc.Description != "" {
		parts = append(parts, "desc="+svc.Description)
	}
	if len(svc.DependsOn) > 0 {
		parts = append(parts, "deps="+strings.Join(uniqueSortedStrings(svc.DependsOn), ","))
	}
	return strings.Join(parts, " ")
}

func containerChanged(oldContainer, newContainer discovery.ContainerInfo) bool {
	return oldContainer.Image != newContainer.Image ||
		oldContainer.Status != newContainer.Status ||
		oldContainer.State != newContainer.State ||
		!sameStringSliceIgnoreOrder(portStrings(oldContainer.Ports), portStrings(newContainer.Ports)) ||
		!sameStringSliceIgnoreOrder(oldContainer.Volumes, newContainer.Volumes) ||
		!sameStringSliceIgnoreOrder(oldContainer.Networks, newContainer.Networks) ||
		!sameStringMap(oldContainer.Labels, newContainer.Labels)
}

func containerSeverityForAddition(container discovery.ContainerInfo) string {
	if isRunningContainer(container) {
		return severityInfo
	}
	return severityWarning
}

func containerSeverityForRemoval(container discovery.ContainerInfo) string {
	if isRunningContainer(container) {
		return severityCritical
	}
	return severityWarning
}

func containerSeverityForModification(oldContainer, newContainer discovery.ContainerInfo) string {
	if isRunningContainer(oldContainer) && !isRunningContainer(newContainer) {
		return severityCritical
	}
	if !isRunningContainer(oldContainer) && isRunningContainer(newContainer) {
		return severityInfo
	}
	return severityWarning
}

func isRunningContainer(container discovery.ContainerInfo) bool {
	return strings.EqualFold(container.State, "running") || strings.HasPrefix(strings.ToLower(container.Status), "up ") || strings.EqualFold(container.Status, "running")
}

func formatContainer(container discovery.ContainerInfo) string {
	parts := []string{
		"state=" + emptyFallback(container.State),
		"status=" + emptyFallback(container.Status),
		"image=" + emptyFallback(container.Image),
	}
	if len(container.Ports) > 0 {
		parts = append(parts, "ports="+strings.Join(portStrings(container.Ports), ","))
	}
	if len(container.Volumes) > 0 {
		parts = append(parts, "volumes="+strings.Join(uniqueSortedStrings(container.Volumes), ","))
	}
	if len(container.Networks) > 0 {
		parts = append(parts, "networks="+strings.Join(uniqueSortedStrings(container.Networks), ","))
	}
	if len(container.Labels) > 0 {
		parts = append(parts, "labels="+formatStringMap(container.Labels))
	}
	return strings.Join(parts, " ")
}

func comparePorts(oldPort, newPort discovery.OpenPort) bool {
	return oldPort.Protocol != newPort.Protocol ||
		oldPort.Address != newPort.Address ||
		oldPort.Process != newPort.Process ||
		oldPort.PID != newPort.PID
}

func portChanged(oldPort, newPort discovery.OpenPort) bool {
	return comparePorts(oldPort, newPort)
}

func portKey(port discovery.OpenPort) string {
	return fmt.Sprintf("%d/%s", port.Port, strings.ToLower(port.Protocol))
}

func portDisplayName(port discovery.OpenPort) string {
	return fmt.Sprintf("%d/%s", port.Port, strings.ToLower(port.Protocol))
}

func formatOpenPort(port discovery.OpenPort) string {
	parts := []string{portDisplayName(port)}
	if port.Address != "" {
		parts = append(parts, "addr="+port.Address)
	}
	if port.Process != "" {
		parts = append(parts, "proc="+port.Process)
	}
	if port.PID != 0 {
		parts = append(parts, "pid="+strconv.Itoa(port.PID))
	}
	return strings.Join(parts, " ")
}

func compareDiskPartitions(oldDisk, newDisk discovery.DiskPartition) bool {
	return oldDisk.Filesystem != newDisk.Filesystem ||
		oldDisk.SizeGB != newDisk.SizeGB ||
		oldDisk.UsedGB != newDisk.UsedGB ||
		oldDisk.AvailGB != newDisk.AvailGB ||
		oldDisk.UsePercent != newDisk.UsePercent
}

func diskChanged(oldDisk, newDisk discovery.DiskPartition) bool {
	return compareDiskPartitions(oldDisk, newDisk)
}

func diskSeverityForModification(oldDisk, newDisk discovery.DiskPartition) string {
	if newDisk.UsePercent >= 90 || newDisk.UsePercent-oldDisk.UsePercent >= 15 {
		return severityCritical
	}
	return severityWarning
}

func formatDisk(disk discovery.DiskPartition) string {
	return fmt.Sprintf("fs=%s size=%.1fGB used=%.1fGB avail=%.1fGB used%%=%.1f mount=%s",
		emptyFallback(disk.Filesystem), disk.SizeGB, disk.UsedGB, disk.AvailGB, disk.UsePercent, emptyFallback(disk.MountPoint))
}

func sameStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func formatStringMap(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ",")
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func snapshotTime(record *discovery.SnapshotRecord) time.Time {
	if record == nil {
		return time.Time{}
	}
	if !record.CapturedAt.IsZero() {
		return record.CapturedAt
	}
	if record.Snapshot != nil {
		return record.Snapshot.CapturedAt
	}
	return time.Time{}
}

func snapshotTimeFromSnapshot(snapshot *discovery.ServerSnapshot) time.Time {
	if snapshot == nil {
		return time.Time{}
	}
	return snapshot.CapturedAt
}

func timestampString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func emptyFallback(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func portStrings(ports []discovery.PortMapping) []string {
	if len(ports) == 0 {
		return nil
	}
	result := make([]string, 0, len(ports))
	for _, port := range ports {
		result = append(result, fmt.Sprintf("%d->%d/%s", port.HostPort, port.ContainerPort, strings.ToLower(port.Protocol)))
	}
	sort.Strings(result)
	return result
}
