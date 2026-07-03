package migration

import (
	"context"
	"fmt"
	"time"
)

// ObservationEngine monitors the target server after cutover.
// It performs periodic health checks and triggers automatic rollback
// if error thresholds are exceeded.
type ObservationEngine struct {
	health        *HealthEngine
	cutoverEngine *CutoverEngine
	repo          PipelineRepo
}

// NewObservationEngine creates a new observation engine.
func NewObservationEngine(health *HealthEngine, repo PipelineRepo, cutoverEngine ...*CutoverEngine) *ObservationEngine {
	var engine *CutoverEngine
	if len(cutoverEngine) > 0 {
		engine = cutoverEngine[0]
	}
	return &ObservationEngine{
		health:        health,
		cutoverEngine: engine,
		repo:          repo,
	}
}

// ObservationConfig configures post-cutover observation.
type ObservationConfig struct {
	MigrationID         int                 `json:"migrationId"`
	Duration            time.Duration       `json:"duration"`
	CheckInterval       time.Duration       `json:"checkInterval"`
	HealthChecks        []HealthCheckConfig `json:"healthChecks"`
	MaxErrorRate        float64             `json:"maxErrorRate"`
	MaxLatencyMs        int64               `json:"maxLatencyMs"`
	MinHealthScore      float64             `json:"minHealthScore"`
	AutoRollback        bool                `json:"autoRollback"`
	ConsecutiveFailures int                 `json:"consecutiveFailures"`
}

// DefaultObservationConfig returns sensible defaults.
func DefaultObservationConfig(migrationID int) ObservationConfig {
	return ObservationConfig{
		MigrationID:         migrationID,
		Duration:            10 * time.Minute,
		CheckInterval:       30 * time.Second,
		MaxErrorRate:        0.05,
		MaxLatencyMs:        5000,
		MinHealthScore:      80,
		AutoRollback:        true,
		ConsecutiveFailures: 3,
	}
}

// Observe runs the observation loop after cutover.
// It checks health at regular intervals and returns an error if
// thresholds are exceeded, triggering automatic rollback.
func (e *ObservationEngine) Observe(ctx context.Context, config ObservationConfig) error {
	if e == nil {
		return fmt.Errorf("observation engine is nil")
	}
	if e.health == nil {
		return fmt.Errorf("health engine not configured")
	}
	if e.repo == nil {
		return fmt.Errorf("pipeline repo not configured")
	}

	if config.Duration == 0 {
		config.Duration = 10 * time.Minute
	}
	if config.CheckInterval == 0 {
		config.CheckInterval = 30 * time.Second
	}
	if config.MaxErrorRate == 0 {
		config.MaxErrorRate = 0.05
	}
	if config.MinHealthScore == 0 {
		config.MinHealthScore = 80
	}
	if config.ConsecutiveFailures == 0 {
		config.ConsecutiveFailures = 3
	}

	deadlineTimer := time.NewTimer(config.Duration)
	defer deadlineTimer.Stop()

	ticker := time.NewTicker(config.CheckInterval)
	defer ticker.Stop()

	consecutiveFailures := 0
	thresholdBreached := false
	var breachErr error

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-deadlineTimer.C:
			if thresholdBreached {
				return breachErr
			}
			return nil

		case <-ticker.C:
			// Run health checks
			results, score := e.health.CheckAll(ctx, config.HealthChecks)

			// Record health check results
			for i := range results {
				results[i].MigrationID = config.MigrationID
				e.repo.CreateHealthCheckResult(ctx, results[i])
			}

			// Record metrics
			e.repo.CreateMetric(ctx, MigrationMetric{
				MigrationID: config.MigrationID,
				MetricName:  "health_score",
				MetricValue: score.Score,
				MetricUnit:  "percent",
				StageName:   "observation",
			})
			e.repo.CreateMetric(ctx, MigrationMetric{
				MigrationID: config.MigrationID,
				MetricName:  "error_rate",
				MetricValue: score.ErrorRate,
				MetricUnit:  "ratio",
				StageName:   "observation",
			})
			e.repo.CreateMetric(ctx, MigrationMetric{
				MigrationID: config.MigrationID,
				MetricName:  "avg_response_ms",
				MetricValue: score.AvgResponseMs,
				MetricUnit:  "ms",
				StageName:   "observation",
			})

			// Check thresholds
			thresholdOK := e.checkThresholds(score, config)
			if !thresholdOK {
				consecutiveFailures++
				if consecutiveFailures >= config.ConsecutiveFailures {
					thresholdBreached = true
					if breachErr == nil {
						breachErr = fmt.Errorf("observation failed: %d consecutive threshold breaches (score=%.1f, error_rate=%.3f, avg_latency=%.0fms)",
							consecutiveFailures, score.Score, score.ErrorRate, score.AvgResponseMs)
					}
					if config.AutoRollback {
						if e.cutoverEngine == nil {
							return fmt.Errorf("%w (auto rollback requested but cutover engine not configured)", breachErr)
						}
						if err := e.cutoverEngine.Rollback(ctx, config.MigrationID); err != nil {
							return fmt.Errorf("%v (rollback failed: %w)", breachErr, err)
						}
						return breachErr
					}
				}
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

// CheckThresholds checks if the current health metrics are within acceptable thresholds.
func (e *ObservationEngine) CheckThresholds(ctx context.Context, migrationID int, config ObservationConfig) (bool, error) {
	if e.health == nil || len(config.HealthChecks) == 0 {
		return true, nil
	}

	_, score := e.health.CheckAll(ctx, config.HealthChecks)
	return e.checkThresholds(score, config), nil
}

// checkThresholds evaluates health score against configured thresholds.
func (e *ObservationEngine) checkThresholds(score HealthScore, config ObservationConfig) bool {
	if score.Score < config.MinHealthScore {
		return false
	}
	if score.ErrorRate > config.MaxErrorRate {
		return false
	}
	if config.MaxLatencyMs > 0 && score.AvgResponseMs > float64(config.MaxLatencyMs) {
		return false
	}
	return true
}
