package nexusbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/uvwt/agentdock/internal/audittrust"
	"github.com/uvwt/agentdock/internal/nexusclient"
)

const AuditTrustCheckpointSnapshotContract = "audit-trust-checkpoint-snapshot/v1"

type AuditTrustCheckpointSnapshot struct {
	Contract   string                `json:"contract"`
	NodeID     string                `json:"node_id"`
	Checkpoint audittrust.Checkpoint `json:"checkpoint"`
	Registry   audittrust.Registry   `json:"registry"`
}

type auditTrustCheckpointResponse struct {
	OK              bool `json:"ok"`
	TrustCheckpoint struct {
		NodeID     string                `json:"node_id"`
		Checkpoint audittrust.Checkpoint `json:"checkpoint"`
		Registry   audittrust.Registry   `json:"registry"`
	} `json:"trust_checkpoint"`
}

func PublishAuditTrustCheckpoint(ctx context.Context, agentDockHome string, checkpoint audittrust.Checkpoint, registry audittrust.Registry) (AuditTrustCheckpointSnapshot, error) {
	identity, err := Load(agentDockHome)
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("load paired Nexus node identity: %w", err)
	}
	if checkpoint.Statement.NodeID != identity.NodeID || checkpoint.Statement.DeviceID != identity.DeviceID {
		return AuditTrustCheckpointSnapshot{}, errors.New("audit trust checkpoint subject does not match the paired Nexus identity")
	}
	if registry.Subject.NodeID != identity.NodeID || registry.Subject.DeviceID != identity.DeviceID {
		return AuditTrustCheckpointSnapshot{}, errors.New("audit trust registry subject does not match the paired Nexus identity")
	}
	body, err := json.Marshal(map[string]any{"checkpoint": checkpoint, "registry": registry})
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("encode audit trust checkpoint publication: %w", err)
	}
	client := nexusclient.New(identity.Endpoint, identity.DeviceToken)
	response, err := client.Do(ctx, http.MethodPut, "/v1/nodes/trust-checkpoint", body)
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("publish audit trust checkpoint to Nexus: %w", err)
	}
	defer response.Body.Close()
	return decodeAuditTrustCheckpointResponse(response, identity.NodeID)
}

func FetchAuditTrustCheckpoint(ctx context.Context, agentDockHome, nodeID string) (AuditTrustCheckpointSnapshot, error) {
	identity, err := Load(agentDockHome)
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("load paired Nexus node identity: %w", err)
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		nodeID = identity.NodeID
	}
	client := nexusclient.New(identity.Endpoint, identity.DeviceToken)
	response, err := client.Do(ctx, http.MethodGet, "/v1/trust-checkpoints/"+url.PathEscape(nodeID), nil)
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("fetch audit trust checkpoint from Nexus: %w", err)
	}
	defer response.Body.Close()
	return decodeAuditTrustCheckpointResponse(response, nodeID)
}

func decodeAuditTrustCheckpointResponse(response *http.Response, expectedNodeID string) (AuditTrustCheckpointSnapshot, error) {
	data, err := nexusclient.ReadBoundedBody(response.Body, 1<<20)
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("read Nexus audit trust checkpoint response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("Nexus audit trust checkpoint request failed (HTTP %d): %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var payload auditTrustCheckpointResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("decode Nexus audit trust checkpoint response: %w", err)
	}
	if !payload.OK || strings.TrimSpace(payload.TrustCheckpoint.NodeID) == "" {
		return AuditTrustCheckpointSnapshot{}, errors.New("Nexus audit trust checkpoint response is incomplete")
	}
	if expectedNodeID = strings.TrimSpace(expectedNodeID); expectedNodeID != "" && payload.TrustCheckpoint.NodeID != expectedNodeID {
		return AuditTrustCheckpointSnapshot{}, errors.New("Nexus audit trust checkpoint response returned the wrong node identity")
	}
	if payload.TrustCheckpoint.Checkpoint.Statement.NodeID != payload.TrustCheckpoint.NodeID || payload.TrustCheckpoint.Registry.Subject.NodeID != payload.TrustCheckpoint.NodeID {
		return AuditTrustCheckpointSnapshot{}, errors.New("Nexus audit trust checkpoint response contains inconsistent node identities")
	}
	return AuditTrustCheckpointSnapshot{
		Contract:   AuditTrustCheckpointSnapshotContract,
		NodeID:     payload.TrustCheckpoint.NodeID,
		Checkpoint: payload.TrustCheckpoint.Checkpoint,
		Registry:   payload.TrustCheckpoint.Registry,
	}, nil
}

func LoadAuditTrustCheckpointSnapshot(path string) (AuditTrustCheckpointSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("read audit trust checkpoint snapshot: %w", err)
	}
	var snapshot AuditTrustCheckpointSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return AuditTrustCheckpointSnapshot{}, fmt.Errorf("decode audit trust checkpoint snapshot: %w", err)
	}
	if snapshot.Contract != AuditTrustCheckpointSnapshotContract {
		return AuditTrustCheckpointSnapshot{}, errors.New("unsupported audit trust checkpoint snapshot contract")
	}
	return snapshot, nil
}

func MarshalAuditTrustCheckpointSnapshot(snapshot AuditTrustCheckpointSnapshot) ([]byte, error) {
	if snapshot.Contract != AuditTrustCheckpointSnapshotContract {
		return nil, errors.New("unsupported audit trust checkpoint snapshot contract")
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
