package installers

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchClaudeSettingsAddsHTTPHooksAndPreservesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	existing := []byte(`{
  "permissions": {"allow": ["Bash(go test ./...)"]},
  "hooks": {
    "UserPromptSubmit": [
      {"hooks": [{"type": "http", "url": "http://old/api/v1/hooks/claude"}]}
    ]
  }
}`)
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := patchClaudeSettings(path, "http://127.0.0.1:6060/")
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["permissions"]; !ok {
		t.Fatalf("expected existing top-level config to be preserved: %s", out)
	}

	hooks := cfg["hooks"].(map[string]any)
	for _, eventName := range []string{"UserPromptSubmit", "SessionStart"} {
		entries := hooks[eventName].([]any)
		if len(entries) == 0 {
			t.Fatalf("expected %s hook entry", eventName)
		}
		first := entries[0].(map[string]any)
		cmds := first["hooks"].([]any)
		hook := cmds[0].(map[string]any)
		if hook["type"] != "http" {
			t.Fatalf("expected http hook for %s, got %#v", eventName, hook)
		}
		if hook["url"] != "http://127.0.0.1:6060/api/v1/hooks/claude" {
			t.Fatalf("unexpected hook url for %s: %#v", eventName, hook["url"])
		}
	}
}

func TestPatchCodexHooksDeduplicatesOpenContextScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	script := filepath.Join(dir, "codex.sh")
	existing := []byte(`{
  "UserPromptSubmit": [
    {"hooks": [{"type": "command", "command": "/other/hook.sh"}]},
    {"hooks": [{"type": "command", "command": "` + script + `"}]}
  ]
}`)
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := patchCodexHooks(path, script)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string][]json.RawMessage
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	entries := cfg["UserPromptSubmit"]
	if got, want := len(entries), 2; got != want {
		t.Fatalf("expected deduplicated hook list length %d, got %d: %s", want, got, out)
	}
	if !strings.Contains(string(entries[0]), script) {
		t.Fatalf("expected OpenContext hook to be prepended: %s", entries[0])
	}
}

func TestInstallGitInstallsWrappersAndBacksUpExistingHooks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")

	hooksDir := filepath.Join(repo, ".git", "hooks")
	existingHook := filepath.Join(hooksDir, "post-commit")
	existingScript := "#!/usr/bin/env bash\necho existing\n"
	if err := os.WriteFile(existingHook, []byte(existingScript), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := InstallGit(repo, "http://127.0.0.1:6060", 2); err != nil {
		t.Fatalf("InstallGit() error = %v", err)
	}

	for _, hook := range []string{"post-commit", "post-checkout", "post-merge", "pre-push"} {
		data, err := os.ReadFile(filepath.Join(hooksDir, hook))
		if err != nil {
			t.Fatalf("expected %s hook wrapper: %v", hook, err)
		}
		if !strings.Contains(string(data), "OpenContext git collector") {
			t.Fatalf("expected OpenContext marker in %s hook: %s", hook, data)
		}
	}

	backupPath := filepath.Join(hooksDir, ".opencontext-backup", "post-commit")
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("expected existing hook backup: %v", err)
	}
	if string(backup) != existingScript {
		t.Fatalf("backup content = %q, want %q", backup, existingScript)
	}

	if err := UninstallGit(repo); err != nil {
		t.Fatalf("UninstallGit() error = %v", err)
	}
	restored, err := os.ReadFile(existingHook)
	if err != nil {
		t.Fatalf("expected restored post-commit hook: %v", err)
	}
	if string(restored) != existingScript {
		t.Fatalf("restored hook = %q, want %q", restored, existingScript)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
