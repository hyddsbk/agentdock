package interop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const AdoptionPlanSchema = "project-due-diligence/adoption-plan/v1"

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type AdoptionSourceSnapshot struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Immutable bool   `json:"immutable"`
}

type AdoptionRequirements struct {
	UnresolvedConditions []string `json:"unresolved_conditions"`
}

type AdoptionDecisionBinding struct {
	ReviewDigest string `json:"review_digest"`
}

type AdoptionPlan struct {
	Schema           string                  `json:"schema"`
	Subject          string                  `json:"subject"`
	Verdict          string                  `json:"verdict"`
	SourceSnapshot   AdoptionSourceSnapshot  `json:"source_snapshot"`
	ApprovedUnits    []string                `json:"approved_units"`
	Requirements     AdoptionRequirements    `json:"requirements"`
	ForbiddenActions []string                `json:"forbidden_actions"`
	ExecutionAllowed bool                    `json:"execution_allowed"`
	DecisionBinding  AdoptionDecisionBinding `json:"decision_binding"`
}

type EphemeralAuthorization struct {
	Skill              string       `json:"skill"`
	SourceLock         SourceLock   `json:"source_lock"`
	SourceLockSHA256   string       `json:"source_lock_sha256"`
	AdoptionPlan       AdoptionPlan `json:"adoption_plan"`
	AdoptionPlanSHA256 string       `json:"adoption_plan_sha256"`
}

func ValidateEphemeralAuthorization(skill string, lock SourceLock, lockSHA string, planRaw []byte, planSHA string) (EphemeralAuthorization, error) {
	skill = strings.TrimSpace(skill)
	if skill == "" {
		return EphemeralAuthorization{}, errors.New("skill is required")
	}
	lockSHA = strings.ToLower(strings.TrimSpace(lockSHA))
	planSHA = strings.ToLower(strings.TrimSpace(planSHA))
	if !sha256Pattern.MatchString(lockSHA) {
		return EphemeralAuthorization{}, errors.New("source_lock_sha256 must be 64 lowercase hex")
	}
	if !sha256Pattern.MatchString(planSHA) {
		return EphemeralAuthorization{}, errors.New("adoption_plan_sha256 must be 64 lowercase hex")
	}
	if err := validateSourceLockForEphemeral(skill, lock); err != nil {
		return EphemeralAuthorization{}, err
	}
	actualLockSHA, err := SourceLockSHA256(lock)
	if err != nil {
		return EphemeralAuthorization{}, fmt.Errorf("hash source lock: %w", err)
	}
	if actualLockSHA != lockSHA {
		return EphemeralAuthorization{}, errors.New("source_lock_sha256 does not match source_lock")
	}

	var plan AdoptionPlan
	var canonical any
	if err := json.Unmarshal(planRaw, &canonical); err != nil {
		return EphemeralAuthorization{}, fmt.Errorf("decode adoption_plan: %w", err)
	}
	if err := json.Unmarshal(planRaw, &plan); err != nil {
		return EphemeralAuthorization{}, fmt.Errorf("decode adoption_plan contract: %w", err)
	}
	actualPlanSHA, err := canonicalJSONSHA256(canonical)
	if err != nil {
		return EphemeralAuthorization{}, fmt.Errorf("hash adoption_plan: %w", err)
	}
	if actualPlanSHA != planSHA {
		return EphemeralAuthorization{}, errors.New("adoption_plan_sha256 does not match canonical adoption_plan")
	}
	if err := validateAdoptionPlanForEphemeral(skill, lock, plan); err != nil {
		return EphemeralAuthorization{}, err
	}
	return EphemeralAuthorization{
		Skill:              skill,
		SourceLock:         lock,
		SourceLockSHA256:   lockSHA,
		AdoptionPlan:       plan,
		AdoptionPlanSHA256: planSHA,
	}, nil
}

func CanonicalJSONSHA256(value any) (string, error) {
	return canonicalJSONSHA256(value)
}

func canonicalJSONSHA256(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validateSourceLockForEphemeral(skill string, lock SourceLock) error {
	if lock.Schema != DiscoveryLockSchema {
		return fmt.Errorf("source_lock schema must be %s", DiscoveryLockSchema)
	}
	if lock.Provider != "github" {
		return errors.New("ephemeral binding currently requires a GitHub source lock")
	}
	if !lock.ReviewRequired || lock.InstallAuthorized || lock.NextOwner != "project-due-diligence" {
		return errors.New("source_lock does not preserve project-due-diligence governance")
	}
	if strings.TrimSpace(lock.Skill) == "" || lock.Skill != skill {
		return errors.New("source_lock skill must exactly match the requested skill")
	}
	if lock.Source.Kind != SourceGitHub || !lock.Source.Immutable || !commitPattern.MatchString(lock.Source.Commit) {
		return errors.New("source_lock must contain an immutable full-commit GitHub SourceRef")
	}
	if lock.Repository != lock.Source.Owner+"/"+lock.Source.Repo {
		return errors.New("source_lock repository does not match its SourceRef")
	}
	return nil
}

func validateAdoptionPlanForEphemeral(skill string, lock SourceLock, plan AdoptionPlan) error {
	if plan.Schema != AdoptionPlanSchema {
		return fmt.Errorf("adoption_plan schema must be %s", AdoptionPlanSchema)
	}
	if plan.Verdict != "ADOPT" {
		return errors.New("ephemeral binding requires an ADOPT verdict")
	}
	if !plan.ExecutionAllowed {
		return errors.New("adoption_plan does not allow execution")
	}
	if plan.SourceSnapshot.Kind != "commit" || !plan.SourceSnapshot.Immutable || strings.ToLower(strings.TrimSpace(plan.SourceSnapshot.Value)) != lock.Source.Commit {
		return errors.New("adoption_plan source_snapshot must match the pinned source commit")
	}
	if len(plan.Requirements.UnresolvedConditions) != 0 {
		return errors.New("adoption_plan has unresolved conditions")
	}
	approved := false
	for _, unit := range plan.ApprovedUnits {
		if strings.TrimSpace(unit) == skill {
			approved = true
			break
		}
	}
	if !approved {
		return errors.New("requested skill is not an exact approved_unit in adoption_plan")
	}
	if !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(plan.DecisionBinding.ReviewDigest))) {
		return errors.New("adoption_plan decision_binding.review_digest must be a SHA-256 digest")
	}
	return nil
}
