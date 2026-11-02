package task

import (
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestCompactTaskSummaryMarksAllStepsDoneAsReviewReady(t *testing.T) {
	task := taskstate.Task{
		ID:     "tsk_review_ready",
		Title:  "Review-ready task",
		Status: taskstate.StatusActive,
		Phase:  taskstate.PhaseExecute,
		Steps: []taskstate.TaskStep{
			{ID: "inspect", Title: "Inspect", Status: taskstate.StepCompleted},
			{ID: "verify", Title: "Verify", Status: taskstate.StepCompleted},
		},
	}
	summary := compactTaskSummary(task)
	if got, ok := summary["review_ready"].(bool); !ok || !got {
		t.Fatalf("review_ready = %#v, want true", summary["review_ready"])
	}
	if summary["status"] != taskstate.StatusActive || summary["phase"] != taskstate.PhaseExecute || summary["review_status"] != "not_started" {
		t.Fatalf("review readiness must be derived without mutating persistent lifecycle: %#v", summary)
	}

	task.Status = taskstate.StatusBlocked
	summary = compactTaskSummary(task)
	if got, ok := summary["review_ready"].(bool); !ok || got {
		t.Fatalf("blocked task review_ready = %#v, want false", summary["review_ready"])
	}

	task.Status = taskstate.StatusActive
	task.FinalReview = &taskstate.FinalReview{Status: taskstate.FinalReviewFailed}
	summary = compactTaskSummary(task)
	if got, ok := summary["review_ready"].(bool); !ok || got {
		t.Fatalf("already-reviewed task review_ready = %#v, want false", summary["review_ready"])
	}
}
