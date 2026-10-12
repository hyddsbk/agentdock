package nexusbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
)

func TestAuditTrustCheckpointPublishAndFetch(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 12, 9, 45, 0, 0, time.UTC)
	registry, err := audittrust.Init(home, audittrust.Subject{NodeID: "node_checkpoint_bridge", DeviceID: "device_checkpoint_bridge"}, now)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, registry, err := audittrust.CreateCheckpoint(home, 2*time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	var published bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-device-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/nodes/trust-checkpoint":
			var input struct {
				Checkpoint audittrust.Checkpoint `json:"checkpoint"`
				Registry   audittrust.Registry   `json:"registry"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.Checkpoint.Statement.RegistryRevision != registry.Revision || input.Registry.RootKeyID != registry.RootKeyID {
				t.Fatalf("publish payload = %#v", input)
			}
			published = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"trust_checkpoint": map[string]any{
					"node_id": "node_checkpoint_bridge", "checkpoint": input.Checkpoint, "registry": input.Registry,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/trust-checkpoints/node_checkpoint_bridge":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"trust_checkpoint": map[string]any{
					"node_id": "node_checkpoint_bridge", "checkpoint": checkpoint, "registry": registry,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := Save(home, Identity{
		Version: identityVersion, Endpoint: server.URL, NodeID: "node_checkpoint_bridge",
		DeviceID: "device_checkpoint_bridge", DeviceToken: "test-device-token",
	}); err != nil {
		t.Fatal(err)
	}
	publishedSnapshot, err := PublishAuditTrustCheckpoint(t.Context(), home, checkpoint, registry)
	if err != nil {
		t.Fatal(err)
	}
	if !published || publishedSnapshot.Contract != AuditTrustCheckpointSnapshotContract || publishedSnapshot.NodeID != "node_checkpoint_bridge" {
		t.Fatalf("published snapshot = %#v", publishedSnapshot)
	}
	fetched, err := FetchAuditTrustCheckpoint(t.Context(), home, "node_checkpoint_bridge")
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Checkpoint.Statement.RegistrySHA256 != checkpoint.Statement.RegistrySHA256 || fetched.Registry.RootKeyID != registry.RootKeyID {
		t.Fatalf("fetched snapshot = %#v", fetched)
	}
}

func TestAuditTrustCheckpointSnapshotRejectsWrongNodeResponse(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"trust_checkpoint": map[string]any{
				"node_id":    "node_other",
				"checkpoint": map[string]any{"statement": map[string]any{"node_id": "node_other"}},
				"registry":   map[string]any{"subject": map[string]any{"node_id": "node_other"}},
			},
		})
	}))
	defer server.Close()
	if err := Save(home, Identity{
		Version: identityVersion, Endpoint: server.URL, NodeID: "node_local",
		DeviceID: "device_local", DeviceToken: "test-device-token",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchAuditTrustCheckpoint(t.Context(), home, "node_expected"); err == nil {
		t.Fatal("wrong-node checkpoint response was accepted")
	}
}
