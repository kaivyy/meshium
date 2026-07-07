package migration

import (
	"context"
	"fmt"
	"math"
)

// RiskEngine assesses migration risk based on various factors.
// It produces a risk score (0-100) and classification (low/medium/high/critical).
type RiskEngine struct {
	repo PipelineRepo
}

// NewRiskEngine creates a new risk engine.
func NewRiskEngine(repo PipelineRepo) *RiskEngine {
	return &RiskEngine{repo: repo}
}

// RiskInput provides the data needed for risk assessment.
type RiskInput struct {
	DataSizeBytes        int64   `json:"dataSizeBytes"`
	DatabaseSizeBytes    int64   `json:"databaseSizeBytes"`
	ContainerCount       int     `json:"containerCount"`
	VolumeCount          int     `json:"volumeCount"`
	QueueCount           int     `json:"queueCount"`
	ReplicationAvailable bool    `json:"replicationAvailable"`
	NetworkSpeedMbps     float64 `json:"networkSpeedMbps"`
	SourceCPU            int     `json:"sourceCPU"`
	TargetCPU            int     `json:"targetCPU"`
	SourceRAMMB          int     `json:"sourceRAMMB"`
	TargetRAMMB          int     `json:"targetRAMMB"`
	CompatibilityIssues  int     `json:"compatibilityIssues"`
	CriticalIssues       int     `json:"criticalIssues"`
}

// AssessRisk calculates the risk score and classification for a migration.
func (e *RiskEngine) AssessRisk(ctx context.Context, migrationID int, input RiskInput) (*RiskReport, error) {
	score := e.calculateScore(input)
	class := e.classifyRisk(score)
	downtime := e.EstimateDowntime(input)
	rollbackComplexity := e.assessRollbackComplexity(input)

	report := RiskReport{
		MigrationID:        migrationID,
		RiskScore:          score,
		RiskClass:          class,
		DowntimeEstimate:   downtime,
		DataSizeBytes:      input.DataSizeBytes,
		DatabaseSizeBytes:  input.DatabaseSizeBytes,
		ContainerCount:     input.ContainerCount,
		VolumeCount:        input.VolumeCount,
		RollbackComplexity: rollbackComplexity,
	}

	if _, err := e.repo.CreateRiskReport(ctx, report); err != nil {
		return nil, fmt.Errorf("create risk report: %w", err)
	}

	// Update migration risk
	if err := e.repo.SetMigrationRisk(migrationID, score, string(class)); err != nil {
		return nil, fmt.Errorf("set migration risk: %w", err)
	}

	return &report, nil
}

// calculateScore computes the risk score (0-100) from the input factors.
func (e *RiskEngine) calculateScore(input RiskInput) float64 {
	var score float64

	// Factor 1: Data size (0-20 points)
	// Larger data = higher risk
	dataGB := float64(input.DataSizeBytes) / (1024 * 1024 * 1024)
	switch {
	case dataGB < 1:
		score += 2
	case dataGB < 10:
		score += 5
	case dataGB < 50:
		score += 10
	case dataGB < 100:
		score += 15
	default:
		score += 20
	}

	// Factor 2: Database size (0-20 points)
	// Larger databases = higher risk (harder to replicate)
	dbGB := float64(input.DatabaseSizeBytes) / (1024 * 1024 * 1024)
	switch {
	case dbGB < 1:
		score += 2
	case dbGB < 10:
		score += 5
	case dbGB < 50:
		score += 12
	case dbGB < 100:
		score += 16
	default:
		score += 20
	}

	// Factor 3: Container count (0-15 points)
	switch {
	case input.ContainerCount <= 3:
		score += 3
	case input.ContainerCount <= 10:
		score += 6
	case input.ContainerCount <= 20:
		score += 10
	default:
		score += 15
	}

	// Factor 4: Volume count (0-10 points)
	switch {
	case input.VolumeCount <= 2:
		score += 2
	case input.VolumeCount <= 5:
		score += 4
	case input.VolumeCount <= 10:
		score += 7
	default:
		score += 10
	}

	// Factor 5: Queue count (0-10 points)
	switch {
	case input.QueueCount == 0:
		score += 0
	case input.QueueCount <= 2:
		score += 3
	case input.QueueCount <= 5:
		score += 6
	default:
		score += 10
	}

	// Factor 6: Replication availability (0-10 points)
	if !input.ReplicationAvailable {
		score += 10 // No replication = much higher risk
	}

	// Factor 7: Network speed (0-10 points)
	switch {
	case input.NetworkSpeedMbps >= 1000:
		score += 1
	case input.NetworkSpeedMbps >= 500:
		score += 3
	case input.NetworkSpeedMbps >= 100:
		score += 5
	case input.NetworkSpeedMbps >= 50:
		score += 7
	default:
		score += 10
	}

	// Factor 8: Resource mismatch (0-5 points)
	if input.TargetCPU < input.SourceCPU {
		score += 2
	}
	if input.TargetRAMMB < input.SourceRAMMB {
		score += 3
	}

	// Factor 9: Compatibility issues
	// Each non-critical compatibility issue adds risk. Cap this contribution
	// (these are warnings, not blockers) but allow it to exceed the old flat 5
	// so several issues register meaningfully.
	score += math.Min(float64(input.CompatibilityIssues)*1.5, 15)

	// Factor 10: Critical blockers
	// A hard blocker means the migration cannot safely proceed. The previous
	// math.Min(..., 5) cap let any number of blockers contribute only 5 points,
	// so blockers could never push the score into the >=85 "critical" class.
	// Weight each blocker heavily and floor the score into the critical band
	// whenever at least one blocker exists, while still capping the total at 100.
	if input.CriticalIssues > 0 {
		score += float64(input.CriticalIssues) * 40
		if score < 85 {
			score = 85
		}
	}

	// Cap at 100
	return math.Min(score, 100)
}

// classifyRisk maps a score to a risk class.
func (e *RiskEngine) classifyRisk(score float64) RiskClass {
	switch {
	case score < 30:
		return RiskClassLow
	case score < 60:
		return RiskClassMedium
	case score < 85:
		return RiskClassHigh
	default:
		return RiskClassCritical
	}
}

// EstimateDowntime estimates the migration downtime based on the input factors.
func (e *RiskEngine) EstimateDowntime(input RiskInput) string {
	if input.ReplicationAvailable && input.NetworkSpeedMbps >= 100 {
		// With replication, downtime is just the cutover time
		cutoverSeconds := 5 + input.QueueCount*3 + input.ContainerCount
		switch {
		case cutoverSeconds <= 5:
			return "~0 seconds (zero-downtime)"
		case cutoverSeconds <= 30:
			return fmt.Sprintf("~%d seconds", cutoverSeconds)
		case cutoverSeconds <= 120:
			return fmt.Sprintf("~%d seconds (1-2 minutes)", cutoverSeconds)
		default:
			return fmt.Sprintf("~%d minutes", cutoverSeconds/60+1)
		}
	}

	// Without replication, downtime = data transfer time
	if input.NetworkSpeedMbps <= 0 {
		input.NetworkSpeedMbps = 100
	}
	totalBytes := input.DataSizeBytes + input.DatabaseSizeBytes
	transferSeconds := float64(totalBytes) / (input.NetworkSpeedMbps * 1024 * 1024 / 8)

	switch {
	case transferSeconds <= 30:
		return fmt.Sprintf("~%.0f seconds", transferSeconds)
	case transferSeconds <= 300:
		return fmt.Sprintf("~%.0f seconds (1-5 minutes)", transferSeconds)
	case transferSeconds <= 3600:
		return fmt.Sprintf("~%.0f minutes", transferSeconds/60)
	default:
		return fmt.Sprintf("~%.1f hours", transferSeconds/3600)
	}
}

// assessRollbackComplexity evaluates how complex a rollback would be.
func (e *RiskEngine) assessRollbackComplexity(input RiskInput) string {
	complexity := 0

	if !input.ReplicationAvailable {
		complexity += 3 // Hard to rollback without replication
	}
	if input.ContainerCount > 10 {
		complexity += 2
	}
	if input.VolumeCount > 5 {
		complexity += 2
	}
	if input.QueueCount > 0 {
		complexity += 1
	}
	if input.DatabaseSizeBytes > 10*1024*1024*1024 {
		complexity += 2
	}

	switch {
	case complexity <= 2:
		return "simple"
	case complexity <= 5:
		return "moderate"
	case complexity <= 10:
		return "complex"
	default:
		return "very_complex"
	}
}
