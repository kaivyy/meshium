package planner

import (
	"context"
	"fmt"
	"time"

	"meshium/internal/mod/discovery"
)

// Planner creates a MigrationPlan from source and target server snapshots.
type Planner interface {
	// CreatePlan generates a migration plan from the source and target snapshots.
	// The plan includes ordered steps, estimates, risk assessment, and any
	// warnings or blockers from the compatibility check.
	//
	// If blockers are present, the plan is still returned — the caller decides
	// whether to proceed.
	CreatePlan(ctx context.Context, source, target *discovery.ServerSnapshot) (*MigrationPlan, error)
}

// DefaultPlanner implements the Planner interface using the Phase 4 discovery
// engine's dependency graph and compatibility checker, combined with
// workload-specific step generators, a risk assessor, and an estimator.
type DefaultPlanner struct {
	// graphBuilder builds a dependency graph from a snapshot.
	// Defaults to discovery.BuildDependencyGraph.
	graphBuilder func(*discovery.ServerSnapshot) *discovery.DependencyGraph
	// compatChecker checks compatibility between source and target.
	// Defaults to discovery.CheckCompatibility.
	compatChecker func(*discovery.ServerSnapshot, *discovery.ServerSnapshot) *discovery.CompatibilityReport
	// riskAssessor evaluates risk for steps and the overall plan.
	riskAssessor RiskAssessor
	// estimator provides transfer size and duration estimates.
	estimator Estimator
	// generators maps node types to step generators.
	generators map[string]StepGenerator
}

// NewDefaultPlanner creates a DefaultPlanner with default components.
func NewDefaultPlanner() *DefaultPlanner {
	return &DefaultPlanner{
		graphBuilder:  discovery.BuildDependencyGraph,
		compatChecker: discovery.CheckCompatibility,
		riskAssessor:  NewDefaultRiskAssessor(),
		estimator:     NewDefaultEstimator(),
		generators: map[string]StepGenerator{
			"container": &DockerStepGenerator{},
			"database":  &DatabaseStepGenerator{},
			"service":   &ServiceStepGenerator{},
			"nginx":     &NginxStepGenerator{},
		},
	}
}

// CreatePlan generates a migration plan from source and target snapshots.
//
// Pipeline:
//  1. Run compatibility check → convert blockers/warnings to plan blockers/warnings
//  2. Build dependency graph from source snapshot
//  3. Topological sort → determine step order
//  4. For each node: generate PlannedStep using the appropriate generator
//  5. Compute DependsOn from graph edges
//  6. Estimate each step
//  7. Assess risk per step and overall
//  8. Compute total estimate
func (p *DefaultPlanner) CreatePlan(ctx context.Context, source, target *discovery.ServerSnapshot) (*MigrationPlan, error) {
	if source == nil {
		return nil, fmt.Errorf("source snapshot is nil")
	}
	if target == nil {
		return nil, fmt.Errorf("target snapshot is nil")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1. Run compatibility check
	compatReport := p.compatChecker(source, target)

	// 2. Build dependency graph from source snapshot
	graph := p.graphBuilder(source)

	// 3. Topological sort
	sortedNodes, err := graph.TopologicalSort()
	if err != nil {
		// Dependency cycle detected — this is a critical blocker
		return &MigrationPlan{
			ID:        generatePlanID(),
			CreatedAt: time.Now().UTC(),
			Source:    snapshotToSummary(source),
			Target:    snapshotToSummary(target),
			Steps:     nil,
			RiskLevel: RiskCritical,
			Blockers: []PlanBlocker{
				{
					Code:    "DEPENDENCY_CYCLE",
					Message: "dependency cycle detected in source server — cannot determine safe migration order",
				},
			},
		}, nil
	}

	// 4. Generate steps for each node
	// Build a map of node ID → step order index for DependsOn computation
	nodeOrder := make(map[string]int)
	steps := make([]PlannedStep, 0, len(sortedNodes))

	for i, node := range sortedNodes {
		nodeOrder[node.ID] = i

		generator, ok := p.generators[node.Type]
		if !ok {
			// No generator for this node type — skip it
			continue
		}

		step, genErr := generator.Generate(node, source, target)
		if genErr != nil {
			// Skip steps that fail to generate
			continue
		}
		step.Order = i
		steps = append(steps, *step)
	}

	// 5. Compute DependsOn from graph edges
	// An edge from A → B means "A depends on B", so A's DependsOn includes B's order
	for _, edge := range graph.Edges {
		fromOrder, fromOk := nodeOrder[edge.From]
		toOrder, toOk := nodeOrder[edge.To]
		if !fromOk || !toOk {
			continue
		}
		// Find the step with this order and add the dependency
		for i := range steps {
			if steps[i].Order == fromOrder {
				// Check if dependency already exists
				alreadyDepends := false
				for _, dep := range steps[i].DependsOn {
					if dep == toOrder {
						alreadyDepends = true
						break
					}
				}
				if !alreadyDepends {
					steps[i].DependsOn = append(steps[i].DependsOn, toOrder)
				}
				break
			}
		}
	}

	// 6. Estimate each step
	for i := range steps {
		steps[i].Estimate = p.estimator.EstimateStep(steps[i], source)
	}

	// 7. Assess risk per step
	for i := range steps {
		steps[i].RiskLevel = p.riskAssessor.AssessStep(steps[i], source, target)
	}

	// 8. Convert compatibility report to plan warnings/blockers
	warnings := make([]PlanWarning, 0, len(compatReport.Warnings))
	for _, w := range compatReport.Warnings {
		warnings = append(warnings, PlanWarning{
			Code:    w.Category,
			Message: w.Message,
		})
	}

	blockers := make([]PlanBlocker, 0, len(compatReport.Blockers))
	for _, b := range compatReport.Blockers {
		blockers = append(blockers, PlanBlocker{
			Code:    b.Category,
			Message: b.Message,
		})
	}

	// 9. Build the plan
	plan := &MigrationPlan{
		ID:        generatePlanID(),
		CreatedAt: time.Now().UTC(),
		Source:    snapshotToSummary(source),
		Target:    snapshotToSummary(target),
		Steps:     steps,
		Warnings:  warnings,
		Blockers:  blockers,
	}

	// 10. Compute total estimate
	plan.TotalEstimate = computeTotalEstimate(steps)

	// 11. Assess overall risk
	plan.RiskLevel = p.riskAssessor.AssessOverall(plan)

	return plan, nil
}

// snapshotToSummary converts a ServerSnapshot to a lightweight ServerSummary.
func snapshotToSummary(s *discovery.ServerSnapshot) ServerSummary {
	if s == nil {
		return ServerSummary{}
	}
	return ServerSummary{
		Hostname:    s.OS.Hostname,
		OS:          s.OS.Distro,
		RAMTotalMB:  s.Hardware.RAMTotalMB,
		DiskTotalGB: s.Hardware.DiskTotalGB,
	}
}

// computeTotalEstimate sums up the estimates across all steps.
func computeTotalEstimate(steps []PlannedStep) TransferEstimate {
	var total TransferEstimate
	total.Confidence = 1.0

	for _, step := range steps {
		total.SizeBytes += step.Estimate.SizeBytes
		total.DurationMin += step.Estimate.DurationMin
		total.DurationMax += step.Estimate.DurationMax

		// Overall confidence is the minimum of all step confidences
		if step.Estimate.Confidence < total.Confidence {
			total.Confidence = step.Estimate.Confidence
		}
	}

	if total.SizeBytes == 0 {
		total.Confidence = 0.0
	}

	return total
}

// generatePlanID generates a unique plan ID.
func generatePlanID() string {
	return fmt.Sprintf("plan-%d", time.Now().UnixNano())
}
