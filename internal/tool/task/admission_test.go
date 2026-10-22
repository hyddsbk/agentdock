package task

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func governedAdmission(goal string) *taskstate.AdmissionInput {
	return &taskstate.AdmissionInput{
		Goal:                goal,
		Deliverable:         "reviewed Core change",
		Audience:            "AgentDock maintainers",
		SuccessConditions:   []string{"implementation passes governed verification"},
		FailureConditions:   []string{"task starts before admission and workflow matching"},
		Constraints:         []string{"no production mutation"},
		EvidenceRequirement: "execution_receipt",
		MutationAuthority:   "local_write",
		Complexity:          "substantial",
		WorkflowType:        "development",
		Project:             "agentdock",
		Device:              "test-device",
	}
}

func admitDirect(t *testing.T, service *Service, input *taskstate.AdmissionInput) Result {
	t.Helper()
	result, err := service.Manage(context.Background(), ManageRequest{Action: "admit", Admission: input})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTaskAdmissionGateFailsClosedBeforeCreateAndWorkflowMatch(t *testing.T) {
	service, _ := newTaskTestService(t)

	if _, err := service.Manage(context.Background(), ManageRequest{Action: "create", Title: "bypass"}); err == nil || !strings.Contains(err.Error(), "admission_ref is required") {
		t.Fatalf("direct create bypass was not rejected: %v", err)
	}
	if _, err := service.WorkflowManage(context.Background(), WorkflowRequest{Action: "match"}); err == nil || !strings.Contains(err.Error(), "admission_ref is required") {
		t.Fatalf("workflow match bypass was not rejected: %v", err)
	}

	for name, mutate := range map[string]func(*taskstate.AdmissionInput){
		"resolve": func(input *taskstate.AdmissionInput) { input.DecisionFrontier = []string{"approval is unresolved"} },
		"blocked": func(input *taskstate.AdmissionInput) {
			input.BlockedReasons = []string{"required authority unavailable"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := governedAdmission("govern " + name)
			mutate(input)
			admitted := admitDirect(t, service, input)
			ref := admitted["admission_ref"].(string)
			if _, err := service.WorkflowManage(context.Background(), WorkflowRequest{Action: "match", AdmissionRef: ref}); err == nil || !strings.Contains(err.Error(), "passing admission") {
				t.Fatalf("%s admission entered workflow match: %v", name, err)
			}
		})
	}
}

func TestTaskAdmissionRequiresMatchAndRejectsSemanticDrift(t *testing.T) {
	service, _ := newTaskTestService(t)
	input := governedAdmission("implement standards-first admission")
	admitted := admitDirect(t, service, input)
	ref := admitted["admission_ref"].(string)

	if _, err := service.Manage(context.Background(), ManageRequest{Action: "create", Title: "too early", AdmissionRef: ref}); err == nil || !strings.Contains(err.Error(), "workflow_template_manage match must run") {
		t.Fatalf("create before workflow match was not rejected: %v", err)
	}
	matched, err := service.WorkflowManage(context.Background(), WorkflowRequest{Action: "match", AdmissionRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	if matched["workflow_match_digest"] == "" {
		t.Fatalf("workflow match receipt was not bound: %#v", matched)
	}

	if _, err := service.Manage(context.Background(), ManageRequest{
		Action: "create", Title: "drifted goal", AdmissionRef: ref, Goal: "different goal",
	}); err == nil || !strings.Contains(err.Error(), "task goal cannot differ") {
		t.Fatalf("goal drift was not rejected: %v", err)
	}
	if _, err := service.Manage(context.Background(), ManageRequest{
		Action: "create", Title: "drifted acceptance", AdmissionRef: ref,
		CompletionConditions: []string{"different success condition"},
	}); err == nil || !strings.Contains(err.Error(), "completion_conditions cannot differ") {
		t.Fatalf("acceptance drift was not rejected: %v", err)
	}
	if _, err := service.WorkflowManage(context.Background(), WorkflowRequest{
		Action: "match", AdmissionRef: ref, Type: "deployment",
	}); err == nil || !strings.Contains(err.Error(), "type cannot differ") {
		t.Fatalf("workflow type drift was not rejected: %v", err)
	}
}

func TestTaskAdmissionHappyPathBindsTaskAndPreservesFinalReview(t *testing.T) {
	service, _ := newTaskTestService(t)
	input := governedAdmission("implement admission happy path")
	admitted := admitDirect(t, service, input)
	ref := admitted["admission_ref"].(string)
	match, err := service.WorkflowManage(context.Background(), WorkflowRequest{Action: "match", AdmissionRef: ref})
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.Manage(context.Background(), ManageRequest{
		Action: "create", Title: "Governed task", AdmissionRef: ref,
		Steps: []StepRequest{{ID: "verify", Title: "Verify governed result"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	task, err := service.tasks.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.AdmissionRef != ref || task.AdmissionDigest != admitted["admission_digest"] || task.WorkflowMatchDigest != match["workflow_match_digest"] {
		t.Fatalf("task admission binding = %#v", task)
	}
	if task.Goal != input.Goal || task.Project != input.Project || task.Device != input.Device {
		t.Fatalf("task did not derive frozen identity fields: %#v", task)
	}
	if len(task.Conditions) != 1 || task.Conditions[0].Text != input.SuccessConditions[0] {
		t.Fatalf("task acceptance did not derive from admission: %#v", task.Conditions)
	}

	if _, err := service.Manage(context.Background(), ManageRequest{
		Action: "checkpoint", TaskID: taskID, StepID: "verify", Status: "completed", Summary: "governed result verified",
		ExecutionEvidence: &ExecutionEvidenceRequest{EvidenceRefs: []string{"artifact://task-admission/happy-path"}},
	}); err != nil {
		t.Fatal(err)
	}
	claim := "standards-first admission remained bound through execution"
	if _, err := service.Manage(context.Background(), ManageRequest{
		Action: "final_review", TaskID: taskID, Status: "pass", Summary: "admission lifecycle verified",
		Verified:         []string{claim},
		VerifiedEvidence: []taskstate.ReviewEvidenceBinding{{Claim: claim, StepID: "verify", EvidenceRefs: []string{"artifact://task-admission/happy-path"}}},
		IntentSpec: &taskstate.OutcomeAxis{
			Status: "pass", Evidence: []string{"admission intent preserved"},
			EvidenceBindings: []taskstate.ReviewEvidenceBinding{{Claim: "admission intent preserved", StepID: "verify", EvidenceRefs: []string{"artifact://task-admission/happy-path"}}},
		},
		EngineeringPolicy: &taskstate.OutcomeAxis{
			Status: "pass", Evidence: []string{"standards-first gate enforced"},
			EvidenceBindings: []taskstate.ReviewEvidenceBinding{{Claim: "standards-first gate enforced", StepID: "verify", EvidenceRefs: []string{"artifact://task-admission/happy-path"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "complete", TaskID: taskID}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskAdmissionTamperBlocksExecutionProgress(t *testing.T) {
	service, _ := newTaskTestService(t)
	input := governedAdmission("reject post-create admission tampering")
	admitted := admitDirect(t, service, input)
	ref := admitted["admission_ref"].(string)
	if _, err := service.WorkflowManage(context.Background(), WorkflowRequest{Action: "match", AdmissionRef: ref}); err != nil {
		t.Fatal(err)
	}
	created, err := service.Manage(context.Background(), ManageRequest{
		Action: "create", Title: "Tamper-evident task", AdmissionRef: ref,
		Steps: []StepRequest{{ID: "verify", Title: "Verify"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(service.tasks.Root(), ref+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["contract"].(map[string]any)["audience"] = "tampered audience"
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = service.Manage(context.Background(), ManageRequest{
		Action: "checkpoint", TaskID: created["task_id"].(string), StepID: "verify", Status: "completed", Summary: "must fail",
	})
	if err == nil || !strings.Contains(err.Error(), "task admission binding failed integrity revalidation") {
		t.Fatalf("tampered admission did not fail closed: %v", err)
	}
}

func TestTaskAdmissionSchemaPublishesStandardsFirstContract(t *testing.T) {
	schema := ManageInputSchema(config.Config{NexusEndpoint: "http://nexus.test"})
	props := schema["properties"].(map[string]any)
	for _, field := range []string{"admission", "admission_ref"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("task_manage schema missing %s", field)
		}
	}
	actions := props["action"].(map[string]any)["enum"].([]string)
	for _, expected := range []string{"admit", "get_admission", "create"} {
		found := false
		for _, action := range actions {
			if action == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("task_manage actions missing %s: %#v", expected, actions)
		}
	}
	admission := props["admission"].(map[string]any)
	required := admission["required"].([]string)
	for _, expected := range []string{"goal", "deliverable", "audience", "success_conditions", "failure_conditions", "evidence_requirement", "mutation_authority", "complexity"} {
		found := false
		for _, item := range required {
			if item == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("admission schema missing required %s: %#v", expected, required)
		}
	}
}
