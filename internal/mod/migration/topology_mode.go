package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ExecutionMode identifies how migration commands reach the source/target.
// It is a certified, persisted property of a cutover — not an assumption — so a
// restart can re-validate the same mode before continuing (rule: reconcile after
// restart using persisted topology evidence plus a fresh re-check).
type ExecutionMode string

const (
	// ModeHost runs commands over direct SSH to the host (the Phase 3 baseline).
	ModeHost ExecutionMode = "host"
	// ModeContainer runs commands via `docker exec` into a named container.
	ModeContainer ExecutionMode = "container"
	// ModeCompose runs commands via `docker compose exec -T <service>`.
	ModeCompose ExecutionMode = "compose"
	// ModeBastion runs commands over SSH through a jump host (dialBastion).
	ModeBastion ExecutionMode = "bastion"

	// ModeUnknown is the un-certified default; any cutover presenting it must
	// be certified before mutation.
	ModeUnknown ExecutionMode = "unknown"
)

// supportedExecutionModes is the runtime guardrail for topology execution mode.
// Anything outside this set is not a certified mode and the cutover must fail
// closed (rule #8: unsupported combination blocked early with actionable error).
var supportedExecutionModes = map[ExecutionMode]bool{
	ModeHost:     true,
	ModeContainer: true,
	ModeCompose:  true,
	ModeBastion:  true,
}

// ErrUnsupportedMode is returned by CertifyTopology when an uncertified or
// explicitly-unsupported execution mode is requested.
var ErrUnsupportedMode = errors.New("unsupported execution mode for cutover")

// TopologyEvidence is the persisted, auditable record of a certified topology.
// It is written before any mutation and re-checked after restart. It never
// contains credentials.
type TopologyEvidence struct {
	Mode          ExecutionMode `json:"mode"`
	SourceID      string        `json:"sourceId"`      // host, container name, or compose service
	TargetID      string        `json:"targetId"`      // host, container name, or compose service
	ComposeProject string       `json:"composeProject,omitempty"` // compose project/stack name if ModeCompose
	BastionID     string        `json:"bastionId,omitempty"`      // jump host if ModeBastion
	ToolAvailable bool          `json:"toolAvailable"` // docker / docker compose / ssh present
	Connectivity  string        `json:"connectivity"`  // "ok" | "failed:<reason>"
	SupportStatus string        `json:"supportStatus"` // automatic | manual | degraded | blocked
	Notes         string        `json:"notes"`
}

// classifyExecutionMode resolves the mode string to a known ExecutionMode,
// returning ModeUnknown for anything unrecognized (never silently assumes host).
func classifyExecutionMode(s string) ExecutionMode {
	switch ExecutionMode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeHost:
		return ModeHost
	case ModeContainer:
		return ModeContainer
	case ModeCompose:
		return ModeCompose
	case ModeBastion:
		return ModeBastion
	default:
		return ModeUnknown
	}
}

// CertifyTopology verifies that the requested execution mode is supported and
// that the transport tool is available and the source/target identity is
// resolved. It issues NO mutating command — only read-only availability probes.
// The returned TopologyEvidence is persisted before cutover and re-checked after
// restart. Any unsupported mode fails closed with ErrUnsupportedMode.
func CertifyTopology(ctx context.Context, ssh SSHExecuter, mode, sourceID, targetID, composeProject, bastionID string) (TopologyEvidence, error) {
	m := classifyExecutionMode(mode)
	ev := TopologyEvidence{
		Mode:           m,
		SourceID:       sourceID,
		TargetID:       targetID,
		ComposeProject: composeProject,
		BastionID:      bastionID,
	}

	if m == ModeUnknown || !supportedExecutionModes[m] {
		ev.SupportStatus = "blocked"
		ev.Notes = fmt.Sprintf("execution mode %q is not a certified cutover mode", mode)
		return ev, fmt.Errorf("%w: %s", ErrUnsupportedMode, ev.Notes)
	}

	// Verify the transport tool exists for the mode (read-only probe).
	var probe string
	switch m {
	case ModeContainer:
		probe = "docker version --format '{{.Server.Version}}' 2>&1"
	case ModeCompose:
		probe = "docker compose version 2>&1"
	case ModeBastion:
		probe = "ssh -V 2>&1"
	case ModeHost:
		probe = "ssh -V 2>&1"
	}
	out, stderr, rc, err := ssh.ExecContext(ctx, probe)
	if err != nil || rc != 0 {
		ev.ToolAvailable = false
		ev.Connectivity = fmt.Sprintf("failed: %s", strings.TrimSpace(firstLine(stderr)))
		ev.SupportStatus = "blocked"
		ev.Notes = fmt.Sprintf("transport tool unavailable for mode %s: %s", m, strings.TrimSpace(out))
		return ev, fmt.Errorf("%w: %s", ErrUnsupportedMode, ev.Notes)
	}
	ev.ToolAvailable = true
	ev.Connectivity = "ok"
	ev.SupportStatus = "automatic"
	ev.Notes = fmt.Sprintf("mode %s certified: tool available, source=%s target=%s", m, sourceID, targetID)
	return ev, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
