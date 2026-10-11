package taskstate

import (
	"strings"
	"testing"
)

func quality(kind, outcome, group string, reasons map[string]string) *StepQuality {
	return &StepQuality{Kind: kind, Outcome: outcome, RefactorGroup: group, DependencyReasons: reasons}
}

func TestStepQualityContractAcceptsTracerBulletGraph(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("Tracer bullets", "deliver narrow end-to-end slices", []string{"verified"}, []TaskStepInput{
		{ID: "inspect", Title: "Inspect seam", CompletionConditions: []string{"Target seam is identified."}, Quality: quality(StepKindDiscovery, "A concrete implementation seam is identified.", "", nil)},
		{ID: "slice", Title: "Ship vertical slice", DependsOn: []string{"inspect"}, CompletionConditions: []string{"One end-to-end behavior passes."}, Quality: quality(StepKindVerticalSlice, "One complete user-visible behavior works through the public seam.", "", map[string]string{"inspect": "The implementation seam must be known before the slice is built."})},
		{ID: "verify", Title: "Verify slice", DependsOn: []string{"slice"}, CompletionConditions: []string{"Regression evidence passes."}, Quality: quality(StepKindVerification, "Independent verification proves the slice works.", "", map[string]string{"slice": "Verification requires the completed behavior."})},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Steps[1].Quality == nil || task.Steps[1].Quality.Kind != StepKindVerticalSlice {
		t.Fatalf("quality not preserved: %#v", task.Steps)
	}
}

func TestStepQualityContractRejectsPartialAndUnexplainedEdges(t *testing.T) {
	tests := []struct {
		name  string
		steps []TaskStepInput
		want  string
	}{
		{"partial", []TaskStepInput{
			{ID: "a", Title: "A", CompletionConditions: []string{"A done."}, Quality: quality(StepKindDiscovery, "A fact is known.", "", nil)},
			{ID: "b", Title: "B", DependsOn: []string{"a"}, CompletionConditions: []string{"B done."}},
		}, "partial"},
		{"missing_reason", []TaskStepInput{
			{ID: "a", Title: "A", CompletionConditions: []string{"A done."}, Quality: quality(StepKindDiscovery, "A fact is known.", "", nil)},
			{ID: "b", Title: "B", DependsOn: []string{"a"}, CompletionConditions: []string{"B done."}, Quality: quality(StepKindVerticalSlice, "B behavior works.", "", nil)},
		}, "explain every dependency edge"},
		{"extra_reason", []TaskStepInput{
			{ID: "a", Title: "A", CompletionConditions: []string{"A done."}, Quality: quality(StepKindDiscovery, "A fact is known.", "", map[string]string{"ghost": "not an edge"})},
		}, "explain every dependency edge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := New(t.TempDir())
			_, err := store.Create("bad", "reject", []string{"rejected"}, tt.steps, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v want %q", err, tt.want)
			}
		})
	}
}

func TestStepQualityContractRejectsRedundantDependency(t *testing.T) {
	store, _ := New(t.TempDir())
	_, err := store.Create("redundant", "reject transitive edge", []string{"rejected"}, []TaskStepInput{
		{ID: "a", Title: "A", CompletionConditions: []string{"A done."}, Quality: quality(StepKindDiscovery, "A established.", "", nil)},
		{ID: "b", Title: "B", DependsOn: []string{"a"}, CompletionConditions: []string{"B done."}, Quality: quality(StepKindVerticalSlice, "B works.", "", map[string]string{"a": "B needs A."})},
		{ID: "c", Title: "C", DependsOn: []string{"a", "b"}, CompletionConditions: []string{"C done."}, Quality: quality(StepKindVerification, "C verifies B.", "", map[string]string{"a": "claimed direct blocker", "b": "C needs B."})},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "redundant dependency a") {
		t.Fatalf("err=%v", err)
	}
}

func TestStepQualityContractValidatesWideRefactorExpandMigrateContract(t *testing.T) {
	store, _ := New(t.TempDir())
	valid := []TaskStepInput{
		{ID: "expand", Title: "Expand schema", CompletionConditions: []string{"Old and new forms coexist."}, Quality: quality(StepKindWideRefactorExpand, "Compatibility form exists beside the old form.", "rename", nil)},
		{ID: "migrate-a", Title: "Migrate package A", DependsOn: []string{"expand"}, CompletionConditions: []string{"Package A uses new form."}, Quality: quality(StepKindWideRefactorMigrate, "Package A is migrated while old form remains valid.", "rename", map[string]string{"expand": "Compatibility form must exist first."})},
		{ID: "migrate-b", Title: "Migrate package B", DependsOn: []string{"expand"}, CompletionConditions: []string{"Package B uses new form."}, Quality: quality(StepKindWideRefactorMigrate, "Package B is migrated while old form remains valid.", "rename", map[string]string{"expand": "Compatibility form must exist first."})},
		{ID: "contract", Title: "Remove old form", DependsOn: []string{"migrate-a", "migrate-b"}, CompletionConditions: []string{"Old form is absent and CI passes."}, Quality: quality(StepKindWideRefactorContract, "The obsolete form is removed after all migrations.", "rename", map[string]string{"migrate-a": "Package A must be migrated first.", "migrate-b": "Package B must be migrated first."})},
	}
	if _, err := store.Create("wide", "safe wide refactor", []string{"green"}, valid, nil); err != nil {
		t.Fatal(err)
	}
	bad := append([]TaskStepInput(nil), valid...)
	bad[3].DependsOn = []string{"migrate-a"}
	bad[3].Quality = quality(StepKindWideRefactorContract, "Remove old form.", "rename", map[string]string{"migrate-a": "A first."})
	if _, err := store.Create("wide bad", "reject unsafe contract", []string{"rejected"}, bad, nil); err == nil || !strings.Contains(err.Error(), "must directly depend on migrate step migrate-b") {
		t.Fatalf("err=%v", err)
	}
}
