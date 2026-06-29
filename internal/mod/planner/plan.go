// Package planner implements the Phase 5 Migration Planner.
//
// The planner takes the output of the Phase 4 Discovery Engine
// (ServerSnapshot + DependencyGraph) and produces a concrete MigrationPlan
// with ordered steps that can be directly executed by the Phase 2 Engine.
//
// The planning pipeline:
//  1. Run compatibility check (Phase 4 CompatibilityChecker)
//  2. Build dependency graph from source snapshot
//  3. Topological sort to determine safe migration order
//  4. Generate PlannedStep for each graph node using workload-specific generators
//  5. Estimate transfer size and duration per step
//  6. Assess risk level per step and overall
//  7. Return MigrationPlan with steps, estimates, warnings, and blockers
//
// If blockers are present, CreatePlan still returns the plan — the caller
// decides whether to proceed.
package planner

import (
	"time"
)

// RiskLevel represents the risk level of a migration step or overall plan.
type RiskLevel string

const (
	// RiskLow: routine operation, minimal risk of data loss or downtime.
	RiskLow RiskLevel = "low"
	// RiskMedium: moderate risk, some potential for issues but recoverable.
	RiskMedium RiskLevel = "medium"
	// RiskHigh: significant risk, requires careful execution and verified backups.
	RiskHigh RiskLevel = "high"
	// RiskCritical: severe risk, should not proceed without explicit user override.
	RiskCritical RiskLevel = "critical"
)

// StepType represents the type of workload a planned step handles.
type StepType string

const (
	// StepTypeDockerVolume: transfer Docker container volumes.
	StepTypeDockerVolume StepType = "docker_volume"
	// StepTypeDockerImage: pull/build Docker images on target.
	StepTypeDockerImage StepType = "docker_image"
	// StepTypeDatabase: dump database on source, transfer, restore on target.
	StepTypeDatabase StepType = "database"
	// StepTypeFile: transfer files/directories (non-Docker, non-config).
	StepTypeFile StepType = "file"
	// StepTypeConfig: transfer configuration files.
	StepTypeConfig StepType = "config"
	// StepTypeNginx: copy Nginx config, verify syntax, reload.
	StepTypeNginx StepType = "nginx"
	// StepTypeService: enable and start systemd services on target.
	StepTypeService StepType = "service"
)

// ServerSummary is a lightweight summary of a server for inclusion in the plan.
type ServerSummary struct {
	Hostname    string  `json:"hostname"`
	OS         string  `json:"os"`
	IP         string  `json:"ip"`
	RAMTotalMB int     `json:"ramTotalMb"`
	DiskTotalGB float64 `json:"diskTotalGb"`
}

// TransferEstimate holds the estimated transfer size and duration for a step.
type TransferEstimate struct {
	// SizeBytes is the estimated total bytes to transfer.
	SizeBytes int64 `json:"sizeBytes"`
	// DurationMin is the minimum estimated duration (optimistic).
	DurationMin time.Duration `json:"durationMin"`
	// DurationMax is the maximum estimated duration (pessimistic).
	DurationMax time.Duration `json:"durationMax"`
	// Confidence is the confidence level of the estimate (0.0 to 1.0).
	Confidence float64 `json:"confidence"`
}

// PlanWarning is a non-blocking warning in a migration plan.
// The caller should display warnings to the user but may proceed with execution.
type PlanWarning struct {
	// Code is a machine-readable warning code.
	Code string `json:"code"`
	// Message is a human-readable warning message.
	Message string `json:"message"`
}

// PlanBlocker is a blocking issue in a migration plan.
// If any blockers exist, the plan should not be executed without explicit override.
type PlanBlocker struct {
	// Code is a machine-readable blocker code.
	Code string `json:"code"`
	// Message is a human-readable blocker message.
	Message string `json:"message"`
}

// PlannedStep is a single step in a migration plan.
// Each step corresponds to a node in the dependency graph and contains
// the configuration needed by the bridge to create a MigrationStep.
type PlannedStep struct {
	// Order is the execution order (0-based, from topological sort).
	Order int `json:"order"`
	// Name is a unique, human-readable name for this step.
	Name string `json:"name"`
	// Type is the workload type (docker_volume, database, file, etc.).
	Type StepType `json:"type"`
	// DependsOn lists the order indices of steps that must complete
	// before this step can start. Derived from the dependency graph edges.
	DependsOn []int `json:"dependsOn,omitempty"`
	// Estimate is the estimated transfer size and duration.
	Estimate TransferEstimate `json:"estimate"`
	// RiskLevel is the assessed risk for this step.
	RiskLevel RiskLevel `json:"riskLevel"`
	// Reversible indicates whether this step can be rolled back.
	Reversible bool `json:"reversible"`
	// Config holds workload-specific configuration used by the bridge
	// to create the corresponding MigrationStep. The shape depends on Type.
	Config map[string]interface{} `json:"config,omitempty"`
}

// MigrationPlan is the output of the planner. It contains an ordered list
// of steps with estimates, risk assessment, and any warnings or blockers.
//
// If Blockers is non-empty, the plan should not be executed without
// explicit user override. The caller decides whether to proceed.
type MigrationPlan struct {
	// ID is a unique identifier for this plan.
	ID string `json:"id"`
	// CreatedAt is when the plan was generated.
	CreatedAt time.Time `json:"createdAt"`
	// Source is a summary of the source server.
	Source ServerSummary `json:"source"`
	// Target is a summary of the target server.
	Target ServerSummary `json:"target"`
	// Steps is the ordered list of planned steps.
	Steps []PlannedStep `json:"steps"`
	// TotalEstimate is the aggregate estimate across all steps.
	TotalEstimate TransferEstimate `json:"totalEstimate"`
	// RiskLevel is the overall risk level of the plan.
	RiskLevel RiskLevel `json:"riskLevel"`
	// Warnings are non-blocking issues the user should be aware of.
	Warnings []PlanWarning `json:"warnings,omitempty"`
	// Blockers are blocking issues that prevent migration.
	// If non-empty, the plan should not be executed without override.
	Blockers []PlanBlocker `json:"blockers,omitempty"`
}

// HasBlockers returns true if the plan has any blocking issues.
func (p *MigrationPlan) HasBlockers() bool {
	return len(p.Blockers) > 0
}

// StepCount returns the number of steps in the plan.
func (p *MigrationPlan) StepCount() int {
	return len(p.Steps)
}

// MigrationPlanSummary is a lightweight summary for listing plans.
type MigrationPlanSummary struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Source    string    `json:"source"`
	Target    string    `json:"target"`
	StepCount int       `json:"stepCount"`
	RiskLevel RiskLevel `json:"riskLevel"`
	HasBlockers bool    `json:"hasBlockers"`
}
