package observ

import (
	"sort"
	"time"
)

// PhaseSpan summarizes everything that happened in one lifecycle phase of a
// migration: when it started and ended, its derived status, and the events that
// occurred within it. It is the unit a UI renders as a row in the execution
// timeline.
type PhaseSpan struct {
	Phase     Phase     `json:"phase"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Status    string    `json:"status"` // started | completed | failed
	Events    []Event   `json:"events"`
	EventCount int      `json:"eventCount"`
}

// Duration is the span's wall-clock length.
func (s PhaseSpan) Duration() time.Duration { return s.End.Sub(s.Start) }

// Timeline is the ordered execution timeline for a single migration: one span
// per phase that saw at least one event, in lifecycle order.
type Timeline struct {
	MigrationID int         `json:"migrationId"`
	Phases      []PhaseSpan `json:"phases"`
}

// Span status constants derived from the audit events within a phase.
const (
	StatusStarted   = "started"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// BuildTimeline groups a migration's events into per-phase spans. It considers
// only events whose MigrationID matches; events for other migrations are
// ignored so a caller can pass a shared MemorySink's full history.
//
// Ordering: spans are sorted by canonical lifecycle rank (see orderedPhases),
// then by first-event time as a tie-break, so the result is deterministic
// regardless of event arrival order. Within a span, events keep sequence order.
//
// Status derivation, in precedence order: a phase with any AuditPhaseFailed
// event is "failed"; otherwise a phase with an AuditPhaseCompleted event is
// "completed"; otherwise "started". This means a failure is never masked by a
// later completion event and an in-flight phase reads as "started".
func BuildTimeline(migrationID int, events []Event) Timeline {
	byPhase := make(map[Phase][]Event)
	for _, e := range events {
		if e.MigrationID != migrationID {
			continue
		}
		byPhase[e.Phase] = append(byPhase[e.Phase], e)
	}

	spans := make([]PhaseSpan, 0, len(byPhase))
	for phase, evs := range byPhase {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].Sequence < evs[j].Sequence })

		span := PhaseSpan{
			Phase:      phase,
			Start:      evs[0].Time,
			End:        evs[0].Time,
			Status:     StatusStarted,
			Events:     evs,
			EventCount: len(evs),
		}
		failed, completed := false, false
		for _, e := range evs {
			if e.Time.Before(span.Start) {
				span.Start = e.Time
			}
			if e.Time.After(span.End) {
				span.End = e.Time
			}
			switch e.Type {
			case AuditPhaseFailed:
				failed = true
			case AuditPhaseCompleted:
				completed = true
			}
		}
		switch {
		case failed:
			span.Status = StatusFailed
		case completed:
			span.Status = StatusCompleted
		}
		spans = append(spans, span)
	}

	sort.SliceStable(spans, func(i, j int) bool {
		ri, rj := phaseRank(spans[i].Phase), phaseRank(spans[j].Phase)
		if ri != rj {
			return ri < rj
		}
		return spans[i].Start.Before(spans[j].Start)
	})

	return Timeline{MigrationID: migrationID, Phases: spans}
}
