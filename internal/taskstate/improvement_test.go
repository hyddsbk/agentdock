package taskstate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func newImprovementTestTask(t *testing.T, mode string) (*Store, Task) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.createWithTraceability(
		"", "", "",
		"Recursive improvement", "improve the first-party harness", "agentdock-core", "",
		[]string{"composed refinement is regression verified"},
		nil, nil, nil,
		&ImprovementPlanInput{
			Target:        "skill-authoring",
			Mode:          mode,
			ProbeCriteria: []string{"same task succeeds", "no authority collision", "no always-on prompt bloat"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return store, task
}

func registerImprovementCandidate(t *testing.T, store *Store, taskID, scope, description, artifactRef, parentID string) (Task, ImprovementCandidate) {
	t.Helper()
	blindSum := sha256.Sum256([]byte(artifactRef))
	task, err := store.UpdateImprovement(taskID, ImprovementUpdate{
		Action: "register_candidate",
		Candidate: &ImprovementCandidateInput{
			Scope: scope, Description: description, ArtifactRef: artifactRef,
			BlindArtifactRef: fmt.Sprintf("artifact://anon-%x", blindSum), ParentID: parentID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := task.ImprovementExperiment.Candidates[len(task.ImprovementExperiment.Candidates)-1]
	return task, candidate
}

func probeImprovementCandidate(t *testing.T, store *Store, taskID, candidateID, summary, ref string) Task {
	t.Helper()
	task, err := store.UpdateImprovement(taskID, ImprovementUpdate{
		Action: "record_probe",
		Probe: &ImprovementProbeInput{
			CandidateID:  candidateID,
			Summary:      summary,
			EvidenceRefs: []string{ref},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestImprovementParallelBlindSelectionAndComposition(t *testing.T) {
	store, task := newImprovementTestTask(t, ImprovementModeParallel)

	_, base := registerImprovementCandidate(t, store, task.ID, ImprovementScopeBase, "unmodified baseline", "artifact://base", "")
	_, invariant := registerImprovementCandidate(t, store, task.ID, ImprovementScopeInvariant, "generalize trigger placement", "artifact://invariant", "")
	_, project := registerImprovementCandidate(t, store, task.ID, ImprovementScopeProject, "AgentDock-only evidence binding refinement", "artifact://project", "")

	probeImprovementCandidate(t, store, task.ID, base.ID, "baseline completes but repeats always-on guidance", "artifact://probe/base")
	probeImprovementCandidate(t, store, task.ID, invariant.ID, "general candidate reduces repeated guidance", "artifact://probe/invariant")
	task = probeImprovementCandidate(t, store, task.ID, project.ID, "project candidate preserves provenance binding", "artifact://probe/project")

	view, err := ImprovementBlindProjection(task)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Candidates) != 3 || view.Mode != ImprovementModeParallel {
		t.Fatalf("unexpected blind view: %#v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	blindJSON := string(encoded)
	for _, forbidden := range []string{base.ID, invariant.ID, project.ID, "unmodified baseline", "generalize trigger placement", "AgentDock-only evidence binding refinement", "artifact://base", "artifact://invariant", "artifact://project", "artifact://probe/base", "artifact://probe/invariant", "artifact://probe/project", "\"scope\"", "\"parent_id\"", "\"evidence_refs\""} {
		if strings.Contains(blindJSON, forbidden) {
			t.Fatalf("blind view leaked author identity %q: %s", forbidden, blindJSON)
		}
	}

	selectedAliases := []string{invariant.BlindAlias, project.BlindAlias}
	task, err = store.UpdateImprovement(task.ID, ImprovementUpdate{
		Action: "select",
		Selection: &ImprovementSelectionInput{
			Aliases: selectedAliases, Rationale: "anonymous probes preserve behavior with less standing context",
			EvidenceRefs: []string{"artifact://selection/blind"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ImprovementExperiment.Status != ImprovementStatusSelected {
		t.Fatalf("selection status = %s", task.ImprovementExperiment.Status)
	}

	task, err = store.UpdateImprovement(task.ID, ImprovementUpdate{
		Action: "compose",
		Composition: &ImprovementCompositionInput{
			Summary:      "compose the general trigger rule with AgentDock evidence binding",
			Lesson:       "Prefer on-demand instruction surfaces unless a rule must be enforced on every run.",
			ArtifactRef:  "artifact://composed/v1",
			EvidenceRefs: []string{"artifact://regression/composed"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ImprovementExperiment.Status != ImprovementStatusComposed || task.ImprovementExperiment.Composition == nil {
		t.Fatalf("experiment not composed: %#v", task.ImprovementExperiment)
	}
	if got := task.ImprovementExperiment.Composition.SourceCandidateIDs; len(got) != 2 || got[0] != invariant.ID || got[1] != project.ID {
		t.Fatalf("composition source ids = %#v", got)
	}
}

func TestImprovementSequentialRequiresProbedParentAndSingleBlindWinner(t *testing.T) {
	store, task := newImprovementTestTask(t, ImprovementModeSequential)
	_, first := registerImprovementCandidate(t, store, task.ID, ImprovementScopeInvariant, "first refinement", "artifact://seq/1", "")

	if _, err := store.UpdateImprovement(task.ID, ImprovementUpdate{
		Action: "register_candidate",
		Candidate: &ImprovementCandidateInput{
			Scope: ImprovementScopeInvariant, Description: "premature child", ArtifactRef: "artifact://seq/premature", BlindArtifactRef: "artifact://anon-1111111111111111", ParentID: first.ID,
		},
	}); err == nil || !strings.Contains(err.Error(), "must be probed") {
		t.Fatalf("unprobed sequential parent should fail: %v", err)
	}

	probeImprovementCandidate(t, store, task.ID, first.ID, "first pass reveals one remaining trigger issue", "artifact://probe/seq/1")
	_, second := registerImprovementCandidate(t, store, task.ID, ImprovementScopeInvariant, "second refinement", "artifact://seq/2", first.ID)
	task = probeImprovementCandidate(t, store, task.ID, second.ID, "second pass resolves the trigger issue", "artifact://probe/seq/2")

	if _, err := store.UpdateImprovement(task.ID, ImprovementUpdate{
		Action: "register_candidate",
		Candidate: &ImprovementCandidateInput{
			Scope: ImprovementScopeInvariant, Description: "branch from stale parent", ArtifactRef: "artifact://seq/stale-parent", BlindArtifactRef: "artifact://anon-2222222222222222", ParentID: first.ID,
		},
	}); err == nil || !strings.Contains(err.Error(), "latest candidate") {
		t.Fatalf("sequential stale parent should fail: %v", err)
	}

	if _, err := store.UpdateImprovement(task.ID, ImprovementUpdate{
		Action: "select",
		Selection: &ImprovementSelectionInput{
			Aliases: []string{first.BlindAlias, second.BlindAlias}, Rationale: "invalid multi-winner",
			EvidenceRefs: []string{"artifact://selection/invalid"},
		},
	}); err == nil || !strings.Contains(err.Error(), "exactly one alias") {
		t.Fatalf("sequential multi-selection should fail: %v", err)
	}

	task, err := store.UpdateImprovement(task.ID, ImprovementUpdate{
		Action: "select",
		Selection: &ImprovementSelectionInput{
			Aliases: []string{second.BlindAlias}, Rationale: "blind evidence favors the second refinement",
			EvidenceRefs: []string{"artifact://selection/seq"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ImprovementExperiment.Selection.CandidateIDs[0] != second.ID {
		t.Fatalf("blind alias did not resolve to selected candidate")
	}
}

func TestImprovementBlocksPassingFinalReviewBeforeComposition(t *testing.T) {
	store, task := newImprovementTestTask(t, ImprovementModeParallel)
	_, err := store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "premature pass"})
	if err == nil || !strings.Contains(err.Error(), "requires improvement_experiment to reach composed state") {
		t.Fatalf("premature passing final review should be blocked: %v", err)
	}
}
