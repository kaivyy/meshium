package migration

import "testing"

func TestNewRollbackManager(t *testing.T) {
	registry := NewCategoryRegistry()
	rm := NewRollbackManager(registry, nil, nil, nil, nil, nil)
	if rm == nil {
		t.Error("expected non-nil rollback manager")
	}
}
