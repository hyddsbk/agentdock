package taskstate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func validAdmissionInput() AdmissionInput {
	return AdmissionInput{
		Goal:                "ship a governed change",
		Deliverable:         "reviewed implementation",
		Audience:            "maintainers",
		SuccessConditions:   []string{"focused tests pass", "acceptance contract remains frozen"},
		FailureConditions:   []string{"execution begins before standards are frozen"},
		Constraints:         []string{"no production mutation"},
		EvidenceRequirement: "execution_receipt",
		MutationAuthority:   "local_write",
		Complexity:          "substantial",
		WorkflowType:        "development",
		Project:             "agentdock",
	}
}

func TestAdmissionClassifiesPassResolveAndBlocked(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	pass, err := store.CreateAdmission(validAdmissionInput())
	if err != nil {
		t.Fatal(err)
	}
	if pass.Status != AdmissionPass || pass.ID == "" || pass.Digest == "" {
		t.Fatalf("pass admission = %#v", pass)
	}

	resolveInput := validAdmissionInput()
	resolveInput.DecisionFrontier = []string{"which migration strategy is approved?"}
	resolve, err := store.CreateAdmission(resolveInput)
	if err != nil {
		t.Fatal(err)
	}
	if resolve.Status != AdmissionResolve {
		t.Fatalf("resolve status = %s", resolve.Status)
	}

	blockedInput := validAdmissionInput()
	blockedInput.DecisionFrontier = []string{"still unresolved"}
	blockedInput.BlockedReasons = []string{"required authority is unavailable"}
	blocked, err := store.CreateAdmission(blockedInput)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != AdmissionBlocked {
		t.Fatalf("blocked status = %s", blocked.Status)
	}

	for _, admission := range []Admission{resolve, blocked} {
		if _, err := store.RecordAdmissionWorkflowMatch(admission.ID, WorkflowMatchReceiptInput{Type: "development"}); err == nil {
			t.Fatalf("non-pass admission %s entered workflow match", admission.ID)
		}
	}
}

func TestAdmissionIsImmutableAndTamperEvident(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admission, err := store.CreateAdmission(validAdmissionInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.saveAdmissionLocked(admission); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected immutable admission rejection, got %v", err)
	}

	data, err := os.ReadFile(store.admissionPath(admission.ID))
	if err != nil {
		t.Fatal(err)
	}
	var tampered Admission
	if err := json.Unmarshal(data, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Contract.Deliverable = "silently changed deliverable"
	data, err = json.MarshalIndent(tampered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.admissionPath(admission.ID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAdmission(admission.ID); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected admission tamper detection, got %v", err)
	}
}

func TestWorkflowMatchReceiptIsAdmissionBoundAndTamperEvident(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admission, err := store.CreateAdmission(validAdmissionInput())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.RecordAdmissionWorkflowMatch(admission.ID, WorkflowMatchReceiptInput{
		Type: "development", Recommended: "plain_task", CandidateIDs: []string{"candidate-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AdmissionRef != admission.ID || receipt.AdmissionDigest != admission.Digest || receipt.Digest == "" {
		t.Fatalf("workflow receipt = %#v", receipt)
	}

	data, err := os.ReadFile(store.admissionMatchPath(admission.ID))
	if err != nil {
		t.Fatal(err)
	}
	var tampered WorkflowMatchReceipt
	if err := json.Unmarshal(data, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Recommended = "rewritten-after-match"
	data, err = json.MarshalIndent(tampered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.admissionMatchPath(admission.ID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAdmissionWorkflowMatch(admission.ID); err == nil || !strings.Contains(err.Error(), "receipt digest mismatch") {
		t.Fatalf("expected workflow receipt tamper detection, got %v", err)
	}
}
