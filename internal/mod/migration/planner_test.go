package migration

import (
	"encoding/json"
	"testing"
)

func TestPlannerPlanRequest(t *testing.T) {
	req := PlanRequest{
		SourceServerID: 1,
		TargetServerID: 2,
		Categories:     []string{"packages", "services"},
	}
	raw, _ := json.Marshal(req)
	var decoded PlanRequest
	json.Unmarshal(raw, &decoded)
	if decoded.SourceServerID != 1 {
		t.Error("expected source server ID 1")
	}
	if decoded.TargetServerID != 2 {
		t.Error("expected target server ID 2")
	}
	if len(decoded.Categories) != 2 {
		t.Error("expected 2 categories")
	}
}

func TestNewPlanner(t *testing.T) {
	registry := NewCategoryRegistry()
	p := NewPlanner(registry, nil, nil, nil, nil, nil)
	if p == nil {
		t.Error("expected non-nil planner")
	}
}
