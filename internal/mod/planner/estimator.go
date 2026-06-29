package planner

import (
	"time"

	"meshium/internal/mod/discovery"
)

// Estimator provides transfer size and duration estimates for planned steps.
type Estimator interface {
	// EstimateStep returns a TransferEstimate for the given planned step,
	// using the source snapshot to determine data sizes.
	EstimateStep(step PlannedStep, source *discovery.ServerSnapshot) TransferEstimate
}

// DefaultEstimator provides estimates based on configurable speed assumptions.
//
// Default speed assumptions (can be overridden):
//   - Network transfer: 100 MB/s (inter-VPS)
//   - Database dump:    50 MB/s
//   - rsync overhead:    1.2x (20% overhead for checksums and protocol)
type DefaultEstimator struct {
	// NetworkSpeedBPS is the assumed network transfer speed in bytes per second.
	// Default: 100 MB/s = 100 * 1024 * 1024.
	NetworkSpeedBPS int64
	// DatabaseDumpSpeedBPS is the assumed database dump speed in bytes per second.
	// Default: 50 MB/s = 50 * 1024 * 1024.
	DatabaseDumpSpeedBPS int64
	// RsyncOverhead is the multiplier applied to transfer time for rsync.
	// Default: 1.2 (20% overhead).
	RsyncOverhead float64
}

// NewDefaultEstimator creates a DefaultEstimator with default speed assumptions.
func NewDefaultEstimator() *DefaultEstimator {
	return &DefaultEstimator{
		NetworkSpeedBPS:      100 * 1024 * 1024, // 100 MB/s
		DatabaseDumpSpeedBPS: 50 * 1024 * 1024,  // 50 MB/s
		RsyncOverhead:        1.2,
	}
}

// EstimateStep returns a TransferEstimate for the given step.
// The estimate is based on the step type and the data available in the source snapshot.
func (e *DefaultEstimator) EstimateStep(step PlannedStep, source *discovery.ServerSnapshot) TransferEstimate {
	if source == nil {
		return TransferEstimate{Confidence: 0.0}
	}

	sizeBytes := e.estimateSizeBytes(step, source)
	if sizeBytes <= 0 {
		return TransferEstimate{
			SizeBytes:   0,
			DurationMin: 0,
			DurationMax: 0,
			Confidence:  0.3, // Low confidence when size is unknown
		}
	}

	speed := e.effectiveSpeedBPS(step)
	if speed <= 0 {
		speed = e.NetworkSpeedBPS
	}

	// Duration = size / speed, with overhead for rsync
	overhead := 1.0
	if step.Type == StepTypeFile || step.Type == StepTypeConfig || step.Type == StepTypeDockerVolume {
		overhead = e.RsyncOverhead
	}

	durationMin := time.Duration(float64(sizeBytes) / float64(speed) * float64(time.Second))
	durationMax := time.Duration(float64(durationMin) * overhead * 1.5) // 50% pessimism

	// Minimum 1 second
	if durationMin < time.Second {
		durationMin = time.Second
	}
	if durationMax < time.Second {
		durationMax = time.Second
	}

	return TransferEstimate{
		SizeBytes:   sizeBytes,
		DurationMin: durationMin,
		DurationMax: durationMax,
		Confidence:  e.estimateConfidence(step, source),
	}
}

// estimateSizeBytes estimates the data size for a step based on its type and config.
func (e *DefaultEstimator) estimateSizeBytes(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	switch step.Type {
	case StepTypeDockerVolume:
		return e.estimateDockerVolumeSize(step, source)
	case StepTypeDockerImage:
		return e.estimateDockerImageSize(step, source)
	case StepTypeDatabase:
		return e.estimateDatabaseSize(step, source)
	case StepTypeFile:
		return e.estimateFileSize(step, source)
	case StepTypeConfig:
		return e.estimateConfigSize(step, source)
	case StepTypeNginx:
		return e.estimateNginxSize(step, source)
	case StepTypeService:
		return 0 // Services don't transfer data
	default:
		return 0
	}
}

// estimateDockerVolumeSize estimates the total size of a container's volumes.
func (e *DefaultEstimator) estimateDockerVolumeSize(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	if source.Docker == nil {
		return 0
	}
	name, _ := step.Config["containerName"].(string)
	if name == "" {
		return 0
	}
	for _, c := range source.Docker.Containers {
		if c.Name == name {
			// Estimate: 500MB per volume if we can't determine actual size
			volCount := len(c.Volumes)
			if volCount == 0 {
				return 0
			}
			return int64(volCount) * 500 * 1024 * 1024
		}
	}
	return 0
}

// estimateDockerImageSize estimates the size of a Docker image.
func (e *DefaultEstimator) estimateDockerImageSize(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	if source.Docker == nil {
		return 0
	}
	image, _ := step.Config["image"].(string)
	if image == "" {
		return 0
	}
	for _, img := range source.Docker.Images {
		if img.Repository+":"+img.Tag == image {
			return parseImageSize(img.Size)
		}
	}
	// Default estimate: 500MB per image
	return 500 * 1024 * 1024
}

// estimateDatabaseSize estimates the size of a database dump.
func (e *DefaultEstimator) estimateDatabaseSize(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	dbType, _ := step.Config["type"].(string)
	if dbType == "" {
		return 0
	}
	for _, db := range source.Databases {
		if db.Type == dbType {
			if db.SizeMB > 0 {
				return db.SizeMB * 1024 * 1024
			}
			// Default estimate: 1GB for unknown database size
			return 1 * 1024 * 1024 * 1024
		}
	}
	return 0
}

// estimateFileSize estimates the size of a file/directory transfer.
func (e *DefaultEstimator) estimateFileSize(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	// Use disk usage from source as a rough estimate
	// For specific paths, we don't have exact sizes, so use a default
	path, _ := step.Config["sourcePath"].(string)
	if path == "" {
		return 0
	}
	// Default estimate: 1GB for file transfers
	return 1 * 1024 * 1024 * 1024
}

// estimateConfigSize estimates the size of config file transfers.
func (e *DefaultEstimator) estimateConfigSize(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	// Configs are typically small — estimate 50MB
	return 50 * 1024 * 1024
}

// estimateNginxSize estimates the size of Nginx config transfers.
func (e *DefaultEstimator) estimateNginxSize(step PlannedStep, source *discovery.ServerSnapshot) int64 {
	// Nginx configs are very small — estimate 5MB
	return 5 * 1024 * 1024
}

// effectiveSpeedBPS returns the effective transfer speed for a step type.
func (e *DefaultEstimator) effectiveSpeedBPS(step PlannedStep) int64 {
	switch step.Type {
	case StepTypeDatabase:
		return e.DatabaseDumpSpeedBPS
	default:
		return e.NetworkSpeedBPS
	}
}

// estimateConfidence returns a confidence score (0.0-1.0) for the estimate.
func (e *DefaultEstimator) estimateConfidence(step PlannedStep, source *discovery.ServerSnapshot) float64 {
	switch step.Type {
	case StepTypeDatabase:
		// High confidence if we have the database size
		dbType, _ := step.Config["type"].(string)
		if dbType != "" {
			for _, db := range source.Databases {
				if db.Type == dbType && db.SizeMB > 0 {
					return 0.85
				}
			}
		}
		return 0.4
	case StepTypeDockerImage:
		// High confidence if we have the image size
		image, _ := step.Config["image"].(string)
		if image != "" && source.Docker != nil {
			for _, img := range source.Docker.Images {
				if img.Repository+":"+img.Tag == image && img.Size != "" {
					return 0.8
				}
			}
		}
		return 0.5
	case StepTypeDockerVolume:
		// Low confidence — volume sizes are hard to determine
		return 0.3
	case StepTypeFile:
		return 0.4
	case StepTypeConfig:
		return 0.7
	case StepTypeNginx:
		return 0.8
	case StepTypeService:
		return 0.9 // No data transfer, just service management
	default:
		return 0.5
	}
}

// parseImageSize parses a human-readable image size string (e.g., "350MB", "1.2GB")
// and returns the size in bytes.
func parseImageSize(size string) int64 {
	if size == "" {
		return 0
	}

	// Extract numeric part and unit
	numEnd := 0
	for numEnd < len(size) && (size[numEnd] >= '0' && size[numEnd] <= '9' || size[numEnd] == '.') {
		numEnd++
	}

	if numEnd == 0 {
		return 0
	}

	numStr := size[:numEnd]
	unit := size[numEnd:]

	var multiplier int64 = 1
	switch {
	case contains(unit, "KB"), contains(unit, "kb"):
		multiplier = 1024
	case contains(unit, "MB"), contains(unit, "mb"):
		multiplier = 1024 * 1024
	case contains(unit, "GB"), contains(unit, "gb"):
		multiplier = 1024 * 1024 * 1024
	case contains(unit, "TB"), contains(unit, "tb"):
		multiplier = 1024 * 1024 * 1024 * 1024
	}

	// Parse the numeric part
	var value float64
	for _, c := range numStr {
		if c >= '0' && c <= '9' {
			value = value*10 + float64(c-'0')
		} else if c == '.' {
			// Handle decimal — for simplicity, just use integer part
			break
		}
	}

	return int64(value * float64(multiplier))
}

// contains checks if a string contains a substring (case-sensitive).
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
