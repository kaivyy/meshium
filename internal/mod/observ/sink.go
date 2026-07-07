package observ

import "sync"

// Sink is the pluggable destination for observability data. It is the seam that
// keeps this package vendor-neutral: Meshium's core emits Events and Metrics to
// a Sink, and an adapter (Prometheus, OpenTelemetry, a database, stdout) lives
// behind this interface. Both methods must be safe for concurrent use, must not
// block indefinitely, and must not panic — the Recorder recovers from a
// panicking sink but a well-behaved sink never relies on that.
//
// A Sink that only cares about one signal implements the other as a no-op
// (embed NopSink to get both for free).
type Sink interface {
	// HandleEvent receives a validated, sequenced event.
	HandleEvent(Event)
	// HandleMetric receives a validated metric.
	HandleMetric(Metric)
}

// NopSink discards everything. It is the default so a Recorder constructed
// without a backend is still safe to call. Embed it to implement only one of
// the two Sink methods.
type NopSink struct{}

func (NopSink) HandleEvent(Event)   {}
func (NopSink) HandleMetric(Metric) {}

// MemorySink retains events and metrics in memory. It exists for tests and for
// building an in-process Timeline without a database. It is safe for concurrent
// use. It is NOT a production store — it grows unbounded — so production wiring
// should use a real Sink.
type MemorySink struct {
	mu      sync.Mutex
	events  []Event
	metrics []Metric
}

// NewMemorySink returns an empty MemorySink.
func NewMemorySink() *MemorySink { return &MemorySink{} }

// HandleEvent appends the event.
func (s *MemorySink) HandleEvent(e Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

// HandleMetric appends the metric.
func (s *MemorySink) HandleMetric(m Metric) {
	s.mu.Lock()
	s.metrics = append(s.metrics, m)
	s.mu.Unlock()
}

// Events returns a copy of the retained events, in emission order.
func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// Metrics returns a copy of the retained metrics, in emission order.
func (s *MemorySink) Metrics() []Metric {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Metric, len(s.metrics))
	copy(out, s.metrics)
	return out
}

// MultiSink fans out to several sinks in order. A panicking or slow sink does
// not prevent the others from receiving the signal (the Recorder isolates
// panics; MultiSink simply iterates). Construct with NewMultiSink.
type MultiSink struct {
	sinks []Sink
}

// NewMultiSink returns a Sink that forwards to all given sinks, skipping nils.
func NewMultiSink(sinks ...Sink) *MultiSink {
	filtered := make([]Sink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			filtered = append(filtered, s)
		}
	}
	return &MultiSink{sinks: filtered}
}

// HandleEvent forwards to each sink.
func (m *MultiSink) HandleEvent(e Event) {
	for _, s := range m.sinks {
		s.HandleEvent(e)
	}
}

// HandleMetric forwards to each sink.
func (m *MultiSink) HandleMetric(metric Metric) {
	for _, s := range m.sinks {
		s.HandleMetric(metric)
	}
}
