package driver

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the set of available workload drivers, keyed by WorkloadType.
// It is the lookup point a planner uses to find a driver for a workload and to
// negotiate capabilities before selecting one.
//
// The zero value is not usable; construct with NewRegistry. All methods are
// safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	drivers map[WorkloadType]Driver
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[WorkloadType]Driver)}
}

// Register adds a driver under its advertised workload type. It returns an
// error if the driver's Capabilities().Workload is empty or already registered,
// so a misconfigured driver fails loudly at wiring time rather than silently
// shadowing another.
func (r *Registry) Register(d Driver) error {
	if d == nil {
		return fmt.Errorf("driver: cannot register nil driver")
	}
	wt := d.Capabilities().Workload
	if wt == "" {
		return fmt.Errorf("driver: cannot register driver with empty workload type")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.drivers[wt]; exists {
		return fmt.Errorf("driver: workload %q already registered", wt)
	}
	r.drivers[wt] = d
	return nil
}

// Get returns the driver registered for the given workload type.
func (r *Registry) Get(wt WorkloadType) (Driver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[wt]
	return d, ok
}

// Workloads returns the registered workload types in sorted order, so callers
// (and tests) get deterministic output.
func (r *Registry) Workloads() []WorkloadType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]WorkloadType, 0, len(r.drivers))
	for wt := range r.drivers {
		out = append(out, wt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NegotiationResult explains the outcome of a capability negotiation for one
// driver: whether it satisfies the requested capabilities and, if not, exactly
// which ones it is missing. A planner surfaces Missing to explain a rejection.
type NegotiationResult struct {
	Workload WorkloadType `json:"workload"`
	OK       bool         `json:"ok"`
	Missing  []Capability `json:"missing,omitempty"`
}

// Negotiate checks whether the driver for wt supports all wanted capabilities.
// It returns an error only when no driver is registered for wt; a registered
// driver that lacks capabilities yields OK=false with Missing populated (not an
// error), so a planner can distinguish "no such driver" from "driver present
// but insufficient".
func (r *Registry) Negotiate(wt WorkloadType, want ...Capability) (NegotiationResult, error) {
	d, ok := r.Get(wt)
	if !ok {
		return NegotiationResult{Workload: wt}, fmt.Errorf("driver: no driver registered for workload %q", wt)
	}
	missing := d.Capabilities().Missing(want...)
	return NegotiationResult{
		Workload: wt,
		OK:       len(missing) == 0,
		Missing:  missing,
	}, nil
}

// SelectByCapabilities returns the workload types of every registered driver
// that supports all wanted capabilities, in sorted order. A planner uses this
// to find candidate drivers for a required capability set (e.g. every driver
// that can do live replication). With no wanted capabilities, all registered
// workloads are returned.
func (r *Registry) SelectByCapabilities(want ...Capability) []WorkloadType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []WorkloadType
	for wt, d := range r.drivers {
		if len(d.Capabilities().Missing(want...)) == 0 {
			out = append(out, wt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
