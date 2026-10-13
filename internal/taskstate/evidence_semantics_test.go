package taskstate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceSemanticsPersistsProviderNeutralRecord(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("Evidence semantics", "preserve evidence meaning", []string{"evidence recorded"}, []TaskStepInput{
		{ID: "inspect", Title: "Inspect target", Phase: PhaseExecute},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	task, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepCompleted, "inspection complete",
		nil, nil, nil,
		[]EvidenceRecordInput{{
			Statement:        "The inspected provider returned a bounded call graph.",
			FactStatus:       EvidenceFactObserved,
			SourceRefs:       []string{"artifact://analysis/call-graph"},
			Coverage:         EvidenceCoveragePartial,
			Limitations:      []string{"Indirect runtime edges were not observed."},
			UntrustedContent: true,
			PartialResult:    true,
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Steps[0].EvidenceRecords) != 1 {
		t.Fatalf("evidence records = %#v", task.Steps[0].EvidenceRecords)
	}
	record := task.Steps[0].EvidenceRecords[0]
	if !validOpaqueEvidenceID(record.ID, "ev_") || record.FactStatus != EvidenceFactObserved ||
		record.Coverage != EvidenceCoveragePartial || !record.UntrustedContent || !record.PartialResult {
		t.Fatalf("unexpected evidence record: %#v", record)
	}
	if !containsString(OutcomeEvidenceRefs(task.Steps[0]), "evidence://"+record.ID) {
		t.Fatalf("semantic evidence ref missing from outcome evidence: %#v", OutcomeEvidenceRefs(task.Steps[0]))
	}
}

func TestUnknownResolutionRequiresCurrentRevisionAndOwnedEvidence(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("Unknown ledger", "resolve uncertainty explicitly", []string{"unknown resolved"}, []TaskStepInput{
		{ID: "inspect", Title: "Inspect target", Phase: PhaseExecute},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepInProgress, "gap recorded",
		nil, nil, nil, nil,
		[]UnknownUpdate{{Action: "open", Question: "Does the provider expose indirect runtime edges?", Blocking: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Unknowns) != 1 || task.Unknowns[0].Revision != 1 || task.Unknowns[0].Status != UnknownStatusOpen {
		t.Fatalf("unexpected unknown ledger: %#v", task.Unknowns)
	}
	unknownID := task.Unknowns[0].ID

	task, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepInProgress, "new evidence recorded",
		nil, nil, nil,
		[]EvidenceRecordInput{{
			Statement:  "Runtime capture contains the indirect edge.",
			FactStatus: EvidenceFactObserved,
			SourceRefs: []string{"artifact://runtime/capture"},
			Coverage:   EvidenceCoverageComplete,
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRef := "evidence://" + task.Steps[0].EvidenceRecords[0].ID

	_, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepInProgress, "stale resolution rejected",
		nil, nil, nil, nil,
		[]UnknownUpdate{{Action: "resolve", UnknownID: unknownID, ExpectedRevision: 2, Resolution: "Runtime evidence resolves the edge question.", EvidenceRefs: []string{evidenceRef}}},
	)
	if err == nil || !strings.Contains(err.Error(), "revision mismatch") {
		t.Fatalf("expected revision mismatch, got %v", err)
	}

	task, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepCompleted, "gap resolved",
		nil, nil, nil, nil,
		[]UnknownUpdate{{Action: "resolve", UnknownID: unknownID, ExpectedRevision: 1, Resolution: "Runtime evidence resolves the edge question.", EvidenceRefs: []string{evidenceRef}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.Unknowns[0].Status != UnknownStatusResolved || task.Unknowns[0].Revision != 2 ||
		task.Unknowns[0].ResolvedStepID != "inspect" || len(task.Unknowns[0].ResolutionEvidenceRefs) != 1 {
		t.Fatalf("unexpected resolved unknown: %#v", task.Unknowns[0])
	}
}

func TestFinalReviewCannotSilentlyPassOpenUnknowns(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("Unknown review gate", "prevent unknown from becoming absence", []string{"review complete"}, []TaskStepInput{
		{ID: "inspect", Title: "Inspect target", Phase: PhaseVerify},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepCompleted, "bounded inspection complete",
		nil, nil, nil,
		[]EvidenceRecordInput{{
			Statement:     "No direct edge was observed in the bounded static scan.",
			FactStatus:    EvidenceFactObserved,
			SourceRefs:    []string{"artifact://static/scan"},
			Coverage:      EvidenceCoveragePartial,
			Limitations:   []string{"Dynamic dispatch was outside the scan boundary."},
			PartialResult: true,
		}},
		[]UnknownUpdate{{Action: "open", Question: "Could an unobserved dynamic edge still exist?", Blocking: false}},
	)
	if err != nil {
		t.Fatal(err)
	}
	unknownID := task.Unknowns[0].ID
	evidenceRef := "evidence://" + task.Steps[0].EvidenceRecords[0].ID
	intent, policy := passingBoundAxes("inspect", []string{evidenceRef})
	base := FinalReviewInput{
		Status: FinalReviewPass, Summary: "bounded claim verified",
		VerifiedFacts:    []string{"Only the bounded static observation is claimed."},
		VerifiedEvidence: []ReviewEvidenceBinding{reviewBinding("Only the bounded static observation is claimed.", "inspect", nil, []string{evidenceRef})},
		IntentSpec:       intent, EngineeringPolicy: policy,
	}
	if _, err := store.FinalReview(task.ID, base); err == nil || !strings.Contains(err.Error(), "explicitly acknowledge unresolved unknown") {
		t.Fatalf("expected open unknown acknowledgement gate, got %v", err)
	}
	base.AcknowledgedUnknownIDs = []string{unknownID}
	task, err = store.FinalReview(task.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.FinalReview.AcknowledgedUnknownIDs) != 1 || task.FinalReview.AcknowledgedUnknownIDs[0] != unknownID {
		t.Fatalf("acknowledged unknowns not persisted: %#v", task.FinalReview)
	}
	_, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepCompleted, "should remain frozen",
		nil, nil, nil,
		[]EvidenceRecordInput{{
			Statement:  "A post-review observation must not mutate the reviewed snapshot.",
			FactStatus: EvidenceFactObserved, SourceRefs: []string{"artifact://late"},
			Coverage: EvidenceCoveragePartial,
		}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "already passed final review") {
		t.Fatalf("expected passing review to freeze semantic evidence, got %v", err)
	}
}

func TestFinalReviewRejectsBlockingUnknownEvenWhenAcknowledged(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("Blocking unknown", "block unsupported pass", []string{"review complete"}, []TaskStepInput{
		{ID: "inspect", Title: "Inspect target", Phase: PhaseVerify},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.CheckpointWithEvidenceTDDAndSemantics(
		task.ID, "inspect", StepCompleted, "inspection complete",
		nil, nil, nil, nil,
		[]UnknownUpdate{{Action: "open", Question: "Is production mutation safe?", Blocking: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	intent, policy := passingOutcomeAxes()
	_, err = store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "should fail",
		VerifiedFacts:          []string{"inspection ran"},
		AcknowledgedUnknownIDs: []string{task.Unknowns[0].ID},
		IntentSpec:             intent, EngineeringPolicy: policy,
	})
	if err == nil || !strings.Contains(err.Error(), "blocking unknown") {
		t.Fatalf("expected blocking unknown rejection, got %v", err)
	}
}

func TestTaskSchema10MigratesToEvidenceSemanticsSchema(t *testing.T) {
	data, err := json.Marshal(Task{SchemaVersion: outcomeSchemaVersion, ID: "tsk_0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := decodeTask(data, "legacy-v10")
	if err != nil {
		t.Fatal(err)
	}
	if task.SchemaVersion != SchemaVersion || SchemaVersion != 11 {
		t.Fatalf("schema version = %d want 11", task.SchemaVersion)
	}
}

func TestEvidenceContractRejectsUnknownCompleteAndPartialComplete(t *testing.T) {
	for _, input := range []EvidenceRecordInput{
		{Statement: "Unknown claim", FactStatus: EvidenceFactUnknown, Coverage: EvidenceCoverageComplete},
		{Statement: "Partial claim", FactStatus: EvidenceFactObserved, SourceRefs: []string{"artifact://x"}, Coverage: EvidenceCoverageComplete, PartialResult: true},
	} {
		if _, err := normalizeEvidenceRecordInputs([]EvidenceRecordInput{input}); err == nil {
			t.Fatalf("expected invalid evidence contract: %#v", input)
		}
	}
}
