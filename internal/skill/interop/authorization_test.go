package interop

import (
	"encoding/json"
	"strings"
	"testing"
)

const authCommit = "0123456789abcdef0123456789abcdef01234567"

func validEphemeralAuthorizationFixture(t *testing.T, skill string) (SourceLock, string, map[string]any, string) {
	t.Helper()
	ref, err := ParseSourceRef("github:owner/repo@" + authCommit)
	if err != nil {
		t.Fatal(err)
	}
	lock := SourceLock{
		Schema:            DiscoveryLockSchema,
		Provider:          "github",
		Repository:        "owner/repo",
		Skill:             skill,
		Source:            ref,
		ReviewRequired:    true,
		InstallAuthorized: false,
		NextOwner:         "project-due-diligence",
	}
	lockSHA, err := SourceLockSHA256(lock)
	if err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{
		"schema":  AdoptionPlanSchema,
		"subject": "owner/repo",
		"verdict": "ADOPT",
		"source_snapshot": map[string]any{
			"kind":      "commit",
			"value":     authCommit,
			"immutable": true,
		},
		"approved_units": []string{skill},
		"requirements": map[string]any{
			"unresolved_conditions": []string{},
		},
		"execution_allowed": true,
		"decision_binding": map[string]any{
			"review_digest": strings.Repeat("a", 64),
		},
	}
	planSHA, err := CanonicalJSONSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	return lock, lockSHA, plan, planSHA
}

func TestValidateEphemeralAuthorizationAcceptsExactAdopt(t *testing.T) {
	lock, lockSHA, plan, planSHA := validEphemeralAuthorizationFixture(t, "demo-skill")
	raw, _ := jsonMarshal(plan)
	got, err := ValidateEphemeralAuthorization("demo-skill", lock, lockSHA, raw, planSHA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Skill != "demo-skill" || got.SourceLock.Source.Commit != authCommit {
		t.Fatalf("authorization = %#v", got)
	}
}

func TestValidateEphemeralAuthorizationRejectsNonAdoptAndUnapprovedUnit(t *testing.T) {
	lock, lockSHA, plan, _ := validEphemeralAuthorizationFixture(t, "demo-skill")
	for _, mutate := range []func(map[string]any){
		func(p map[string]any) { p["verdict"] = "ADAPT" },
		func(p map[string]any) { p["approved_units"] = []string{"other-skill"} },
		func(p map[string]any) {
			p["requirements"] = map[string]any{"unresolved_conditions": []string{"needs evidence"}}
		},
		func(p map[string]any) {
			p["source_snapshot"] = map[string]any{"kind": "commit", "value": strings.Repeat("f", 40), "immutable": true}
		},
	} {
		clone := cloneJSONMap(t, plan)
		mutate(clone)
		planSHA, err := CanonicalJSONSHA256(clone)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := jsonMarshal(clone)
		if _, err := ValidateEphemeralAuthorization("demo-skill", lock, lockSHA, raw, planSHA); err == nil {
			t.Fatalf("mutated adoption plan unexpectedly authorized: %#v", clone)
		}
	}
}

func TestValidateEphemeralAuthorizationRejectsDigestMismatch(t *testing.T) {
	lock, lockSHA, plan, planSHA := validEphemeralAuthorizationFixture(t, "demo-skill")
	raw, _ := jsonMarshal(plan)
	if _, err := ValidateEphemeralAuthorization("demo-skill", lock, strings.Repeat("0", 64), raw, planSHA); err == nil {
		t.Fatal("mismatched source lock digest was accepted")
	}
	if _, err := ValidateEphemeralAuthorization("demo-skill", lock, lockSHA, raw, strings.Repeat("0", 64)); err == nil {
		t.Fatal("mismatched adoption plan digest was accepted")
	}
}

func TestValidateEphemeralAuthorizationRequiresLockSkillMatch(t *testing.T) {
	lock, lockSHA, plan, planSHA := validEphemeralAuthorizationFixture(t, "demo-skill")
	lock.Skill = "other-skill"
	lockSHA, _ = SourceLockSHA256(lock)
	raw, _ := jsonMarshal(plan)
	if _, err := ValidateEphemeralAuthorization("demo-skill", lock, lockSHA, raw, planSHA); err == nil {
		t.Fatal("mismatched source-lock skill was accepted")
	}
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
