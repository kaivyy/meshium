package migration

import "testing"

func TestNewExecutor(t *testing.T) {
	registry := NewCategoryRegistry()
	executor := NewExecutor(registry, nil, nil, nil, nil, nil)
	if executor == nil {
		t.Error("expected non-nil executor")
	}
}
