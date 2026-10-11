package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	skillstate "github.com/uvwt/agentdock/internal/skill/state"
)

const InstalledProvenanceBindingSchema = "project-due-diligence/installed-provenance-binding/v1"

var provenanceHex64 = regexp.MustCompile("^[0-9a-f]{64}$")

type BindProvenanceRequest struct {
	Skill                   string
	Version                 string
	PlanSHA256              string
	ActivationReceiptSHA256 string
	ArtifactSHA256          string
	Bundle                  json.RawMessage
}

type BindProvenanceResult struct {
	Skill                   string    `json:"skill"`
	Version                 string    `json:"version"`
	PlanSHA256              string    `json:"plan_sha256"`
	ActivationReceiptSHA256 string    `json:"activation_receipt_sha256"`
	ArtifactSHA256          string    `json:"artifact_sha256"`
	InstalledContentSHA256  string    `json:"installed_content_sha256"`
	BoundAt                 time.Time `json:"bound_at"`
}

func (m *Manager) BindProvenance(ctx context.Context, req BindProvenanceRequest) (BindProvenanceResult, error) {
	skill := strings.TrimSpace(req.Skill)
	version := strings.TrimSpace(req.Version)
	if skill == "" || version == "" {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.input", errors.New("skill and version are required"))
	}
	planSHA, err := strictProvenanceHex(req.PlanSHA256, "plan_sha256")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.input", err)
	}
	receiptSHA, err := strictProvenanceHex(req.ActivationReceiptSHA256, "activation_receipt_sha256")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.input", err)
	}
	artifactSHA, err := strictProvenanceHex(req.ArtifactSHA256, "artifact_sha256")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.input", err)
	}
	if len(req.Bundle) == 0 || len(req.Bundle) > 1<<20 {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.bundle", errors.New("bundle must be a non-empty JSON object no larger than 1 MiB"))
	}
	var bundle map[string]json.RawMessage
	if err := json.Unmarshal(req.Bundle, &bundle); err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.bundle", err)
	}
	if len(bundle["plan"]) == 0 || len(bundle["chain"]) == 0 {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.bundle", errors.New("bundle must contain plan and chain"))
	}

	active, err := m.State.ActiveVersion(skill)
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.active", err)
	}
	if active != version {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.active", fmt.Errorf("skill %s version %s is not active", skill, version))
	}
	packageDir, err := m.State.InstalledPath(skill, version)
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.resolve", err)
	}
	contentDigest, err := digestPackageContent(packageDir)
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.content_digest", err)
	}
	contentSHA, err := prefixedDigestHex(contentDigest, "installed content digest")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.content_digest", err)
	}

	metaPath := filepath.Join(packageDir, ".agentdock-install.json")
	original, err := os.ReadFile(metaPath)
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(original, &metadata); err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	sourceDigest, err := metadataString(metadata, "source_digest")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	sourceSHA, err := prefixedDigestHex(sourceDigest, "source_digest")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	if sourceSHA != artifactSHA {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.artifact", fmt.Errorf("receipt artifact digest does not match installed source package digest"))
	}
	recordedContent, err := metadataString(metadata, "content_digest")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	recordedContentSHA, err := prefixedDigestHex(recordedContent, "content_digest")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	if recordedContentSHA != contentSHA {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.content_digest", errors.New("installed content digest does not match install metadata"))
	}

	boundAt := time.Now().UTC()
	binding := skillstate.ProvenanceBinding{
		Schema:                  InstalledProvenanceBindingSchema,
		PlanSHA256:              planSHA,
		ActivationReceiptSHA256: receiptSHA,
		ArtifactSHA256:          artifactSHA,
		InstalledContentSHA256:  contentSHA,
		Bundle:                  append(json.RawMessage(nil), req.Bundle...),
		BoundAt:                 boundAt,
	}
	bindingJSON, err := json.Marshal(binding)
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	metadata["provenance"] = bindingJSON
	updated, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	if err := atomicfile.Write(metaPath, updated, 0o600); err != nil {
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.metadata", err)
	}
	if err := m.State.BindProvenance(ctx, skill, version, binding); err != nil {
		restoreErr := atomicfile.Write(metaPath, original, 0o600)
		return BindProvenanceResult{}, packageError(ErrProvenanceFailed, "provenance.state", errors.Join(err, restoreErr))
	}

	return BindProvenanceResult{
		Skill:                   skill,
		Version:                 version,
		PlanSHA256:              planSHA,
		ActivationReceiptSHA256: receiptSHA,
		ArtifactSHA256:          artifactSHA,
		InstalledContentSHA256:  contentSHA,
		BoundAt:                 boundAt,
	}, nil
}

func strictProvenanceHex(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if !provenanceHex64.MatchString(value) {
		return "", fmt.Errorf("%s must be 64 lowercase hex", field)
	}
	return value, nil
}

func prefixedDigestHex(value, field string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "sha256:")
	if !provenanceHex64.MatchString(value) {
		return "", fmt.Errorf("%s is not a SHA-256 digest", field)
	}
	return value, nil
}

func metadataString(metadata map[string]json.RawMessage, field string) (string, error) {
	raw := metadata[field]
	if len(raw) == 0 {
		return "", fmt.Errorf("%s is missing; legacy installation cannot be provenance-bound", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is invalid", field)
	}
	return value, nil
}
