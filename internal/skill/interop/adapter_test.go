package interop

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAdapterIDsStable(t *testing.T) {
	want := []string{"claude-code", "codex", "cursor", "gemini-cli", "github-copilot", "opencode", "universal"}
	if got := AdapterIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AdapterIDs() = %#v, want %#v", got, want)
	}
}

func TestOpenCodeRoots(t *testing.T) {
	adapter, ok := Adapter("OpenCode")
	if !ok {
		t.Fatal("opencode adapter missing")
	}
	ctx := PathContext{
		ProjectDir: filepath.Join("C:", "work", "project"),
		HomeDir:    filepath.Join("C:", "Users", "demo"),
		ConfigHome: filepath.Join("C:", "Users", "demo", ".config"),
	}
	project, err := ResolveRoots(adapter.ProjectSkillRoots, ctx)
	if err != nil {
		t.Fatal(err)
	}
	global, err := ResolveRoots(adapter.GlobalSkillRoots, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(project) != 1 || project[0] != filepath.Join(ctx.ProjectDir, ".agents", "skills") {
		t.Fatalf("project roots = %#v", project)
	}
	if len(global) != 1 || global[0] != filepath.Join(ctx.ConfigHome, "opencode", "skills") {
		t.Fatalf("global roots = %#v", global)
	}
}

func TestClaudeConfigEnvRootIsOptionalAndPreferred(t *testing.T) {
	adapter, ok := Adapter("claude-code")
	if !ok {
		t.Fatal("claude-code adapter missing")
	}
	ctx := PathContext{
		HomeDir: filepath.Join("C:", "Users", "demo"),
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR": filepath.Join("D:", "Claude"),
		},
	}
	got, err := ResolveRoots(adapter.GlobalSkillRoots, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join("D:", "Claude", "skills"),
		filepath.Join(ctx.HomeDir, ".claude", "skills"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("global roots = %#v, want %#v", got, want)
	}

	delete(ctx.Env, "CLAUDE_CONFIG_DIR")
	got, err = ResolveRoots(adapter.GlobalSkillRoots, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{filepath.Join(ctx.HomeDir, ".claude", "skills")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback global roots = %#v, want %#v", got, want)
	}
}

func TestResolveRootRejectsEscape(t *testing.T) {
	ctx := PathContext{HomeDir: filepath.Join("C:", "Users", "demo")}
	abs := filepath.Join(string(filepath.Separator), "absolute")
	if vol := filepath.VolumeName(filepath.Clean(ctx.HomeDir)); vol != "" {
		abs = vol + abs
	}
	for _, spec := range []RootSpec{
		{Base: RootHome, Path: "../outside"},
		{Base: RootHome, Path: abs},
		{Base: RootEnv, Path: "skills"},
		{Base: RootHome, EnvVar: "HOME_OVERRIDE", Path: "skills"},
	} {
		if _, _, err := ResolveRoot(spec, ctx); err == nil {
			t.Fatalf("ResolveRoot(%#v) unexpectedly succeeded", spec)
		}
	}
}

func TestAdapterReturnsCopy(t *testing.T) {
	first, ok := Adapter("codex")
	if !ok {
		t.Fatal("codex adapter missing")
	}
	first.ProjectSkillRoots[0].Path = "mutated"
	second, ok := Adapter("codex")
	if !ok {
		t.Fatal("codex adapter missing")
	}
	if second.ProjectSkillRoots[0].Path != ".agents/skills" {
		t.Fatalf("registry mutated through returned adapter: %#v", second)
	}
}
