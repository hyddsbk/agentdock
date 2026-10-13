package mcp

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func TestServeStdioUsesOfficialSDKTransport(t *testing.T) {
	server := NewServer(nil, config.Config{})
	clientInput, serverOutput := io.Pipe()
	serverInput, clientOutput := io.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.ServeStdio(serverInput, serverOutput)
	}()

	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "agentdock-test", Version: "1.0.0"},
		&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
	)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if result := session.InitializeResult(); result == nil || result.ProtocolVersion == "" {
		t.Fatalf("InitializeResult() = %#v", result)
	}
	if err := session.Ping(ctx, nil); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("ServeStdio() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("ServeStdio() did not stop after client close")
	}
}

func TestServeStdioRejectsUninitializedServer(t *testing.T) {
	var server *Server
	if err := server.ServeStdio(nil, nil); err == nil {
		t.Fatal("ServeStdio() accepted an uninitialized server")
	}
}

func TestServeStdioAdvertisesContextAwareInstructions(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.Config
		wantPrefix string
	}{
		{
			name:       "without Nexus",
			cfg:        config.Config{Instructions: "Use absolute paths under /srv."},
			wantPrefix: baseServerInstructions,
		},
		{
			name: "with Nexus",
			cfg: config.Config{
				NexusEndpoint: "http://127.0.0.1:18777",
				Instructions:  "Use absolute paths under /srv.",
			},
			wantPrefix: nexusServerInstructions,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(nil, test.cfg)
			clientInput, serverOutput := io.Pipe()
			serverInput, clientOutput := io.Pipe()
			serverDone := make(chan error, 1)
			go func() {
				serverDone <- server.ServeStdio(serverInput, serverOutput)
			}()

			client := mcpsdk.NewClient(
				&mcpsdk.Implementation{Name: "agentdock-test", Version: "1.0.0"},
				&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
			)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			session, err := client.Connect(ctx, &mcpsdk.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
			if err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
			result := session.InitializeResult()
			if result == nil || !strings.HasPrefix(result.Instructions, test.wantPrefix) {
				t.Fatalf("InitializeResult() = %#v, want %q first", result, test.wantPrefix)
			}
			if !strings.Contains(result.Instructions, "Additional operator instructions:\nUse absolute paths under /srv.") {
				t.Fatalf("InitializeResult() = %#v, want operator instructions appended", result)
			}
			if err := session.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("ServeStdio() error = %v", err)
				}
			case <-ctx.Done():
				t.Fatal("ServeStdio() did not stop after client close")
			}
		})
	}
}

func TestServeStdioHidesNexusExtensionsWithoutNexus(t *testing.T) {
	cfg := config.Config{
		AgentDockDefaultDir: t.TempDir(),
		AgentDockHome:       filepath.Join(t.TempDir(), ".agentdock"),
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()

	server := NewServer(runtime, cfg)
	clientInput, serverOutput := io.Pipe()
	serverInput, clientOutput := io.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ServeStdio(serverInput, serverOutput) }()

	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "agentdock-schema-test", Version: "1.0.0"},
		&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
	)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	var taskManage *mcpsdk.Tool
	for tool, listErr := range session.Tools(ctx, nil) {
		if listErr != nil {
			t.Fatalf("Tools() error = %v", listErr)
		}
		if tool.Name == "evolve" || strings.HasPrefix(tool.Name, "recall_") || tool.Name == "workflow_template_manage" {
			t.Fatalf("Nexus-only tool leaked without Nexus: %s", tool.Name)
		}
		if tool.Name == "task_manage" {
			taskManage = tool
		}
	}
	if taskManage == nil {
		t.Fatal("task_manage should remain available without Nexus")
	}
	encoded, err := json.Marshal(taskManage)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, leaked := range []string{"learning_checks", "source_template_ids", "template_id", "guidance_context", "evolution_candidates", "evolution_warning", "Evolution", "evolution"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("task_manage schema leaked %q without Nexus: %s", leaked, text)
		}
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("ServeStdio() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("ServeStdio() did not stop after client close")
	}
}

func TestFreshMCPClientCanCompleteTwoAxisTaskLifecycle(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()

	server := NewServer(runtime, cfg)
	clientInput, serverOutput := io.Pipe()
	serverInput, clientOutput := io.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ServeStdio(serverInput, serverOutput) }()

	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "agentdock-task-contract-test", Version: "1.0.0"},
		&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
	)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	var taskManage *mcpsdk.Tool
	for tool, listErr := range session.Tools(ctx, nil) {
		if listErr != nil {
			t.Fatalf("Tools() error = %v", listErr)
		}
		if tool.Name == "task_manage" {
			taskManage = tool
			break
		}
	}
	if taskManage == nil {
		t.Fatal("task_manage missing from fresh tools/list")
	}
	encoded, err := json.Marshal(taskManage.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	schemaText := string(encoded)
	for _, field := range []string{"intent_spec", "engineering_policy", "verified_evidence"} {
		if !strings.Contains(schemaText, `"`+field+`"`) {
			t.Fatalf("fresh task_manage schema missing %s: %s", field, schemaText)
		}
	}

	call := func(args map[string]any) map[string]any {
		t.Helper()
		result, callErr := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "task_manage", Arguments: args})
		if callErr != nil {
			t.Fatalf("task_manage %v CallTool() error = %v", args["action"], callErr)
		}
		if result.IsError {
			t.Fatalf("task_manage %v returned tool error: %#v", args["action"], result)
		}
		structured, ok := result.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("task_manage %v structured content = %#v", args["action"], result.StructuredContent)
		}
		return structured
	}

	created := call(map[string]any{
		"action":                "create",
		"title":                 "MCP schema lifecycle",
		"goal":                  "verify fresh MCP task contract",
		"completion_conditions": []string{"fresh MCP final review passes"},
		"steps":                 []map[string]any{{"id": "verify", "title": "Verify"}},
	})
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("create result missing task_id: %#v", created)
	}
	call(map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "verify",
		"status": "completed", "summary": "fresh MCP contract verified",
	})
	call(map[string]any{
		"action": "final_review", "task_id": taskID, "status": "pass", "summary": "two-axis review passed",
		"verified":          []string{"fresh MCP schema accepted two-axis review"},
		"verified_evidence": []map[string]any{{"claim": "fresh MCP schema accepted two-axis review", "step_id": "verify"}},
		"risks":             []string{},
		"intent_spec": map[string]any{
			"status": "pass", "evidence": []string{"intent satisfied"},
			"evidence_bindings": []map[string]any{{"claim": "intent satisfied", "step_id": "verify"}},
		},
		"engineering_policy": map[string]any{
			"status": "pass", "evidence": []string{"policy satisfied"},
			"evidence_bindings": []map[string]any{{"claim": "policy satisfied", "step_id": "verify"}},
		},
	})
	completed := call(map[string]any{"action": "complete", "task_id": taskID})
	summary, _ := completed["task_summary"].(map[string]any)
	if summary["status"] != "completed" {
		t.Fatalf("complete summary = %#v", summary)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("ServeStdio() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("ServeStdio() did not stop after client close")
	}
}
