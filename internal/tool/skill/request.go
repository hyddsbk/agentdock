package skill

// PackageRequest 是 skill_package 进入 Skill capability 后的稳定输入契约。
type PackageRequest struct {
	Action                  string         `json:"action"`
	Skill                   string         `json:"skill,omitempty"`
	Version                 string         `json:"version,omitempty"`
	Key                     string         `json:"key,omitempty"`
	Value                   *string        `json:"value,omitempty"`
	Source                  string         `json:"source,omitempty"`
	Digest                  string         `json:"digest,omitempty"`
	Activate                *bool          `json:"activate,omitempty"`
	MaxBytes                *int           `json:"max_bytes,omitempty"`
	PlanSHA256              string         `json:"plan_sha256,omitempty"`
	ActivationReceiptSHA256 string         `json:"activation_receipt_sha256,omitempty"`
	ArtifactSHA256          string         `json:"artifact_sha256,omitempty"`
	ProvenanceBundle        map[string]any `json:"provenance_bundle,omitempty"`
}

type InspectRequest struct {
	Skill   string `json:"skill"`
	Version string `json:"version,omitempty"`
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
