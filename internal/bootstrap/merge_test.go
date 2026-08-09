package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPreservesRuntimeFieldsAndOwnsDeploymentFields(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	configDir := filepath.Join(home, ".botmux")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `[{"larkAppId":"old","larkAppSecret":"old-secret","cliId":"codex","model":"old","globalGrants":["ou_1"],"quota":{"limit":3}}]`
	if err := os.WriteFile(filepath.Join(configDir, "bots.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(root, "appSecret")
	if err := os.WriteFile(secretFile, []byte("new-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Run(Options{
		Home:          home,
		WorkingDir:    workspace,
		WorkspacePath: workspace,
		AppID:         "cli_new",
		AppSecretFile: secretFile,
		Brand:         "lark",
		CLIID:         "claude-code",
		Backend:       "tmux",
		Sandbox:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var entries []map[string]any
	data, err := os.ReadFile(filepath.Join(configDir, "bots.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got["larkAppId"] != "cli_new" || got["larkAppSecret"] != "new-secret" {
		t.Fatalf("operator fields were not replaced: %#v", got)
	}
	if got["globalGrants"] == nil || got["quota"] == nil {
		t.Fatalf("runtime fields were not preserved: %#v", got)
	}
	if _, exists := got["model"]; exists {
		t.Fatalf("empty managed model should be removed: %#v", got)
	}
	if mode := fileMode(t, filepath.Join(configDir, "bots.json")); mode != 0o600 {
		t.Fatalf("bots.json mode = %o, want 600", mode)
	}
}

func TestRunRejectsInvalidExistingJSON(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configDir := filepath.Join(home, ".botmux")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "bots.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(root, "appSecret")
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{
		Home: home, WorkingDir: filepath.Join(root, "workspace"), WorkspacePath: filepath.Join(root, "workspace"), AppID: "cli_x",
		AppSecretFile: secretFile, Brand: "feishu", CLIID: "codex", Backend: "tmux",
	}); err == nil {
		t.Fatal("expected invalid existing JSON to fail")
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
