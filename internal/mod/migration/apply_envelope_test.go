package migration

import (
	"context"
	"encoding/json"
	"testing"
)

// migration_steps.data stores the ENVELOPE a collector returns:
//
//	{"type":"packages","meta":{…},"data":{"distro":"apt","packages":[…]}}
//
// The apply loop decodes it correctly into CategoryData before handing it to
// the Applier, but applyItemPlan wrapped the raw envelope as if it were
// already the inner payload:
//
//	itemKeysFor(step.Category, CategoryData{Data: json.RawMessage(step.Data)})
//
// json.Unmarshal is lenient about unknown fields, so decoding the envelope
// into PackagesData did NOT error — it just produced a zero-valued struct with
// no packages. Zero item keys → empty apply-set → the whole category recorded
// as "no items selected to apply" → skipped.
//
// Found by the first live full migration (Alibaba → vpsexp7agus): all four
// categories were skipped, nothing was written to the target, and the stage
// still reported "Collected data applied to target" and completed.
func TestItemKeysForUnwrapsTheStoredEnvelope(t *testing.T) {
	inner, err := json.Marshal(PackagesData{
		Distro:   "apt",
		Packages: []string{"nginx", "curl"},
		Count:    2,
	})
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	envelope, err := json.Marshal(CategoryData{Type: "packages", Data: inner})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	keys := itemKeysForStep("packages", string(envelope))
	if len(keys) != 2 {
		t.Fatalf("derived %d item keys from a 2-package plan: %v", len(keys), keys)
	}
	want := map[string]bool{"package:nginx": true, "package:curl": true}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

func TestItemKeysForStepHandlesEveryCategory(t *testing.T) {
	cases := []struct {
		category string
		inner    interface{}
		wantKey  string
	}{
		{"packages", PackagesData{Packages: []string{"nginx"}}, "package:nginx"},
		{"configs", ConfigsData{Files: map[string][]byte{"/etc/a.conf": []byte("x")}}, "config:/etc/a.conf"},
		{"services", ServicesData{Services: []string{"nginx"}}, "service:nginx"},
	}
	for _, tc := range cases {
		inner, _ := json.Marshal(tc.inner)
		env, _ := json.Marshal(CategoryData{Type: tc.category, Data: inner})
		keys := itemKeysForStep(tc.category, string(env))
		found := false
		for _, k := range keys {
			if k == tc.wantKey {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %q missing from %v", tc.category, tc.wantKey, keys)
		}
	}
}

// The whole point of the apply-set is that an undecided plan applies
// everything (legacy default). With the envelope bug the apply-set came out
// empty, which the stage read as "the operator deselected everything".
func TestApplyItemPlanAppliesEverythingWhenUndecided(t *testing.T) {
	inner, _ := json.Marshal(PackagesData{Packages: []string{"nginx", "curl"}, Count: 2})
	env, _ := json.Marshal(CategoryData{Type: "packages", Data: inner})

	pc := &PipelineContext{MigrationID: 1}
	step := MigrationStepRecord{ID: 1, Category: "packages", Action: "collect", Data: string(env)}

	applySet, results := applyItemPlan(context.Background(), pc, step, map[string]ParityAction{}, nil)

	if len(applySet) != 2 {
		t.Fatalf("apply-set has %d items, want 2 — an undecided plan must apply everything", len(applySet))
	}
	if len(results) != 2 {
		t.Fatalf("got %d item results, want 2", len(results))
	}
	for _, r := range results {
		if r.ExecutionState != ExecPending {
			t.Errorf("%s state = %q, want pending", r.ItemKey, r.ExecutionState)
		}
	}
}

// An explicit keep_target must still be honoured — the fix must not turn the
// selection machinery back into "apply everything regardless".
func TestApplyItemPlanHonorsExplicitSelections(t *testing.T) {
	inner, _ := json.Marshal(PackagesData{Packages: []string{"nginx", "curl"}, Count: 2})
	env, _ := json.Marshal(CategoryData{Type: "packages", Data: inner})

	pc := &PipelineContext{MigrationID: 1}
	step := MigrationStepRecord{ID: 1, Category: "packages", Action: "collect", Data: string(env)}
	decisions := map[string]ParityAction{"package:curl": ActionKeepTarget}

	applySet, _ := applyItemPlan(context.Background(), pc, step, decisions, nil)

	if applySet["package:curl"] {
		t.Error("keep_target item was put in the apply-set")
	}
	if !applySet["package:nginx"] {
		t.Error("undecided item was dropped from the apply-set")
	}
}
