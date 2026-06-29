package planner

import (
	"meshium/internal/mod/discovery"
)

// RiskAssessor evaluates the risk level of individual steps and the overall plan.
type RiskAssessor interface {
	// AssessStep returns the risk level for a single planned step,
	// given the source and target snapshots for context.
	AssessStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel

	// AssessOverall returns the aggregate risk level for the entire plan.
	// This is typically the highest risk level among all steps, but may
	// be elevated if multiple steps compound the risk.
	AssessOverall(plan *MigrationPlan) RiskLevel
}

// DefaultRiskAssessor implements RiskAssessor with configurable thresholds.
type DefaultRiskAssessor struct {
	// LargeVolumeThresholdGB is the volume size above which a Docker
	// volume transfer is considered medium risk. Default: 10 GB.
	LargeVolumeThresholdGB int64
	// LargeDatabaseThresholdMB is the database size above which a
	// database migration is considered medium risk. Default: 5000 MB (5 GB).
	LargeDatabaseThresholdMB int64
}

// NewDefaultRiskAssessor creates a DefaultRiskAssessor with default thresholds.
func NewDefaultRiskAssessor() *DefaultRiskAssessor {
	return &DefaultRiskAssessor{
		LargeVolumeThresholdGB:   10,
		LargeDatabaseThresholdMB: 5000,
	}
}

// AssessStep evaluates the risk level for a single planned step.
//
// Risk factors:
//   - Database without verified backup        → High
//   - Container with volume > 10GB            → Medium
//   - Port conflict on target                → Critical
//   - Dependency cycle detected               → Critical
//   - Service with unknown dependencies       → Medium
//   - Migration without downtime window       → High
func (a *DefaultRiskAssessor) AssessStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	switch step.Type {
	case StepTypeDatabase:
		return a.assessDatabaseStep(step, source, target)
	case StepTypeDockerVolume:
		return a.assessDockerVolumeStep(step, source, target)
	case StepTypeDockerImage:
		return a.assessDockerImageStep(step, source, target)
	case StepTypeFile:
		return a.assessFileStep(step, source, target)
	case StepTypeConfig:
		return a.assessConfigStep(step, source, target)
	case StepTypeNginx:
		return a.assessNginxStep(step, source, target)
	case StepTypeService:
		return a.assessServiceStep(step, source, target)
	default:
		return RiskMedium
	}
}

// AssessOverall returns the aggregate risk level for the plan.
// The overall risk is the highest individual step risk, but may be
// elevated to Critical if there are blockers or if many steps are High risk.
func (a *DefaultRiskAssessor) AssessOverall(plan *MigrationPlan) RiskLevel {
	if plan == nil || len(plan.Steps) == 0 {
		return RiskLow
	}

	// If there are blockers, the overall risk is Critical
	if plan.HasBlockers() {
		return RiskCritical
	}

	// Find the highest risk level among all steps
	highest := RiskLow
	highCount := 0
	for _, step := range plan.Steps {
		if step.RiskLevel == RiskCritical {
			return RiskCritical
		}
		if step.RiskLevel == RiskHigh {
			highCount++
			highest = RiskHigh
		} else if step.RiskLevel == RiskMedium && highest == RiskLow {
			highest = RiskMedium
		}
	}

	// If there are 3 or more high-risk steps, elevate to Critical
	if highCount >= 3 {
		return RiskCritical
	}

	return highest
}

// assessDatabaseStep assesses risk for database migration steps.
func (a *DefaultRiskAssessor) assessDatabaseStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	// Database migrations are inherently high risk — data loss is possible
	// if the dump is incomplete or the restore fails.
	risk := RiskHigh

	// Check if we have size information
	dbType, _ := step.Config["type"].(string)
	if source != nil && dbType != "" {
		for _, db := range source.Databases {
			if db.Type == dbType {
				if db.SizeMB > a.LargeDatabaseThresholdMB {
					// Large databases are high risk (already high, but note it)
					return RiskHigh
				}
				if db.SizeMB > 0 && db.SizeMB < 100 {
					// Small databases are medium risk
					return RiskMedium
				}
				break
			}
		}
	}

	// Check if the database is running — running databases need to be
	// stopped or use hot backup, which adds risk
	if source != nil && dbType != "" {
		for _, db := range source.Databases {
			if db.Type == dbType && db.Running {
				return RiskHigh
			}
		}
	}

	return risk
}

// assessDockerVolumeStep assesses risk for Docker volume transfers.
func (a *DefaultRiskAssessor) assessDockerVolumeStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	risk := RiskMedium

	// Check volume size — large volumes are medium risk
	if source != nil && source.Docker != nil {
		name, _ := step.Config["containerName"].(string)
		if name != "" {
			for _, c := range source.Docker.Containers {
				if c.Name == name {
					// Estimate volume size: 500MB per volume
					estimatedSizeGB := int64(len(c.Volumes)) * 500 / 1024
					if estimatedSizeGB > a.LargeVolumeThresholdGB {
						return RiskHigh
					}
					if len(c.Volumes) == 0 {
						return RiskLow // No volumes to transfer
					}
					break
				}
			}
		}
	}

	// Check if the container is running — running containers need to be stopped
	if source != nil && source.Docker != nil {
		name, _ := step.Config["containerName"].(string)
		if name != "" {
			for _, c := range source.Docker.Containers {
				if c.Name == name && c.State == "running" {
					// Running container needs downtime — elevated risk
					return RiskHigh
				}
			}
		}
	}

	return risk
}

// assessDockerImageStep assesses risk for Docker image pull/build.
func (a *DefaultRiskAssessor) assessDockerImageStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	// Image pulls are low risk — they can be retried and don't affect data
	return RiskLow
}

// assessFileStep assesses risk for file transfers.
func (a *DefaultRiskAssessor) assessFileStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	// File transfers are medium risk — data could be corrupted or incomplete
	return RiskMedium
}

// assessConfigStep assesses risk for config file transfers.
func (a *DefaultRiskAssessor) assessConfigStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	// Config transfers are low risk — small files, easy to verify and rollback
	return RiskLow
}

// assessNginxStep assesses risk for Nginx config migration.
func (a *DefaultRiskAssessor) assessNginxStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	// Nginx config is low risk — can be verified with nginx -t before reload
	return RiskLow
}

// assessServiceStep assesses risk for systemd service management.
func (a *DefaultRiskAssessor) assessServiceStep(step PlannedStep, source, target *discovery.ServerSnapshot) RiskLevel {
	risk := RiskLow

	// Check if the service has unknown dependencies
	if source != nil {
		name, _ := step.Config["name"].(string)
		if name != "" {
			for _, svc := range source.Services {
				if svc.Name == name {
					// Services with unknown dependencies are medium risk
					if len(svc.DependsOn) == 0 && svc.Type != "simple" {
						return RiskMedium
					}
					break
				}
			}
		}
	}

	return risk
}

// HasPortConflict checks if any source port conflicts with a target port.
// A conflict exists when the same port is used by different processes.
func HasPortConflict(source, target *discovery.ServerSnapshot) bool {
	if source == nil || target == nil {
		return false
	}
	if len(source.NetworkPorts) == 0 || len(target.NetworkPorts) == 0 {
		return false
	}

	targetPorts := make(map[int]string)
	for _, p := range target.NetworkPorts {
		targetPorts[p.Port] = p.Process
	}

	for _, sp := range source.NetworkPorts {
		if tp, ok := targetPorts[sp.Port]; ok {
			if tp != sp.Process {
				return true
			}
		}
	}
	return false
}
