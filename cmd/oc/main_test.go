package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ohmyctx/opencontext/pkg/event"
)

func TestInstallBrowserChromeCollectorCopiesExtension(t *testing.T) {
	src := t.TempDir()
	target := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), []byte(`{"manifest_version":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "service_worker.js"), []byte(`console.log("ok")`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := installBrowserChromeCollector(src, target, "http://127.0.0.1:6060", false)
	if err != nil {
		t.Fatalf("installBrowserChromeCollector() error = %v", err)
	}
	if result.ExtensionPath != target {
		t.Fatalf("ExtensionPath = %q, want %q", result.ExtensionPath, target)
	}
	if _, err := os.Stat(filepath.Join(target, "manifest.json")); err != nil {
		t.Fatalf("expected copied manifest: %v", err)
	}
	if len(result.NextSteps) == 0 {
		t.Fatal("expected Chrome next steps")
	}
}

func TestSchemaIncludesBrowserChromeInstallFlags(t *testing.T) {
	root := buildRoot()
	cmd, err := findCommandForSchema(root, []string{"collector", "browser-chrome", "install"})
	if err != nil {
		t.Fatalf("findCommandForSchema() error = %v", err)
	}

	schema := buildCommandSchema(cmd)
	if schema.Command != "oc collector browser-chrome install" {
		t.Fatalf("Command = %q", schema.Command)
	}
	if !schemaHasFlag(schema, "--dry-run") {
		t.Fatal("expected --dry-run flag in schema")
	}
	if !schemaHasFlag(schema, "--format") {
		t.Fatal("expected inherited --format flag in schema")
	}
}

func TestCollectorInstallSchemasExposeDryRun(t *testing.T) {
	root := buildRoot()
	for _, path := range [][]string{
		{"collector", "shell", "install"},
		{"collector", "git", "install"},
		{"collector", "claude", "install"},
		{"collector", "codex", "install"},
		{"collector", "cursor", "install"},
		{"collector", "opencode", "install"},
		{"collector", "openclaw", "install"},
		{"collector", "hermes", "install"},
		{"collector", "browser-chrome", "install"},
	} {
		cmd, err := findCommandForSchema(root, path)
		if err != nil {
			t.Fatalf("findCommandForSchema(%v) error = %v", path, err)
		}
		schema := buildCommandSchema(cmd)
		if !schemaHasFlag(schema, "--dry-run") {
			t.Fatalf("expected %s to expose --dry-run", schema.Command)
		}
		if !schemaHasFlag(schema, "--format") {
			t.Fatalf("expected %s to expose --format", schema.Command)
		}
	}
}

func TestSideEffectSchemasExposeRiskMetadata(t *testing.T) {
	root := buildRoot()
	cases := []struct {
		path        []string
		destructive bool
	}{
		{[]string{"daemon", "install"}, false},
		{[]string{"daemon", "uninstall"}, true},
		{[]string{"daemon", "restart"}, true},
		{[]string{"memory", "compile"}, false},
		{[]string{"subscription", "target", "add", "hermes"}, false},
		{[]string{"event", "clear"}, true},
		{[]string{"collector", "git", "uninstall"}, true},
	}
	for _, tc := range cases {
		cmd, err := findCommandForSchema(root, tc.path)
		if err != nil {
			t.Fatalf("findCommandForSchema(%v) error = %v", tc.path, err)
		}
		schema := buildCommandSchema(cmd)
		if !schema.SideEffect {
			t.Fatalf("expected %s to be marked side_effect", schema.Command)
		}
		if !schema.DryRunSupported {
			t.Fatalf("expected %s to support dry-run", schema.Command)
		}
		if schema.Destructive != tc.destructive {
			t.Fatalf("%s destructive = %v, want %v", schema.Command, schema.Destructive, tc.destructive)
		}
	}
}

func TestSchemaDoesNotInheritParentLocalFlags(t *testing.T) {
	root := buildRoot()
	cmd, err := findCommandForSchema(root, []string{"event", "clear"})
	if err != nil {
		t.Fatalf("findCommandForSchema() error = %v", err)
	}
	schema := buildCommandSchema(cmd)
	if schemaHasFlag(schema, "--limit") {
		t.Fatal("did not expect event clear schema to inherit event list --limit flag")
	}
	if !schemaHasFlag(schema, "--source") {
		t.Fatal("expected event clear schema to include its own --source flag")
	}
	if !schemaHasFlag(schema, "--format") {
		t.Fatal("expected event clear schema to include global --format flag")
	}
}

func TestRootSchemaUsesSingularCollectorCommand(t *testing.T) {
	schema := buildCommandSchema(buildRoot())
	if !containsString(schema.Subcommands, "collector") {
		t.Fatal("expected root schema to expose collector command")
	}
	for _, name := range []string{"event", "memory", "subscription"} {
		if !containsString(schema.Subcommands, name) {
			t.Fatalf("expected root schema to expose %q command", name)
		}
	}
	if containsString(schema.Subcommands, "collectors") {
		t.Fatal("did not expect deprecated collectors command in root schema")
	}
	for _, name := range []string{"events", "compile", "inject"} {
		if containsString(schema.Subcommands, name) {
			t.Fatalf("did not expect deprecated top-level %q command in root schema", name)
		}
	}
}

func TestSubscriptionSchemaIncludesDiscoveryVerbs(t *testing.T) {
	root := buildRoot()
	cmd, err := findCommandForSchema(root, []string{"subscription"})
	if err != nil {
		t.Fatalf("findCommandForSchema() error = %v", err)
	}
	schema := buildCommandSchema(cmd)
	for _, name := range []string{"list", "info", "target"} {
		if !containsString(schema.Subcommands, name) {
			t.Fatalf("expected subscription schema to include %q, got %#v", name, schema.Subcommands)
		}
	}
	if schema.SideEffect {
		t.Fatal("subscription inspection should not be marked side_effect")
	}
}

func TestMemorySchemaDoesNotOwnSubscriptionTargets(t *testing.T) {
	root := buildRoot()
	cmd, err := findCommandForSchema(root, []string{"memory"})
	if err != nil {
		t.Fatalf("findCommandForSchema() error = %v", err)
	}
	schema := buildCommandSchema(cmd)
	if containsString(schema.Subcommands, "target") {
		t.Fatalf("did not expect memory schema to expose subscription target management: %#v", schema.Subcommands)
	}
	if !containsString(schema.Subcommands, "compile") {
		t.Fatalf("expected memory schema to expose compile, got %#v", schema.Subcommands)
	}
}

func TestCollectorSchemaIncludesDiscoveryVerbs(t *testing.T) {
	root := buildRoot()
	cmd, err := findCommandForSchema(root, []string{"collector"})
	if err != nil {
		t.Fatalf("findCommandForSchema() error = %v", err)
	}
	schema := buildCommandSchema(cmd)
	for _, name := range []string{"list", "info", "schemas", "shell", "git"} {
		if !containsString(schema.Subcommands, name) {
			t.Fatalf("expected collector schema to include %q, got %#v", name, schema.Subcommands)
		}
	}
}

func TestConfigureOutputModeDefaultsToJSONForNonTTY(t *testing.T) {
	oldFormat := outputFormat
	oldJSON := jsonOut
	defer func() {
		outputFormat = oldFormat
		jsonOut = oldJSON
	}()

	outputFormat = ""
	jsonOut = false
	if err := configureOutputMode(); err != nil {
		t.Fatalf("configureOutputMode() error = %v", err)
	}
	if !jsonOut {
		t.Fatal("expected non-TTY test stdout to default to JSON output")
	}
}

func TestConfigureOutputModeTableOverridesNonTTYDefault(t *testing.T) {
	oldFormat := outputFormat
	oldJSON := jsonOut
	defer func() {
		outputFormat = oldFormat
		jsonOut = oldJSON
	}()

	outputFormat = "table"
	jsonOut = true
	if err := configureOutputMode(); err != nil {
		t.Fatalf("configureOutputMode() error = %v", err)
	}
	if jsonOut {
		t.Fatal("expected --format table to disable JSON output")
	}
}

func TestBuildGitHookEventPostCommit(t *testing.T) {
	repo := initGitRepo(t)
	writeAndCommit(t, repo, "hello.txt", "hello\n", "feat: initial commit")

	e, err := buildGitHookEvent("post-commit", repo, "", "", "", "", "", 2, strings.NewReader(""))
	if err != nil {
		t.Fatalf("buildGitHookEvent() error = %v", err)
	}
	if e.Source != event.SourceGit || e.Type != event.EventTypeCommit {
		t.Fatalf("unexpected event source/type: %s.%s", e.Source, e.Type)
	}
	if e.Labels["repo"] != filepath.Base(repo) {
		t.Fatalf("repo label = %q, want %q", e.Labels["repo"], filepath.Base(repo))
	}
	if e.Payload["message"] != "feat: initial commit" {
		t.Fatalf("message payload = %#v", e.Payload["message"])
	}
	if e.Payload["hash"] == "" {
		t.Fatal("expected commit hash payload")
	}
}

func TestBuildGitHookEventPrePushReadsRefs(t *testing.T) {
	repo := initGitRepo(t)
	writeAndCommit(t, repo, "hello.txt", "hello\n", "feat: initial commit")
	stdin := strings.NewReader("refs/heads/main 0123456789abcdef refs/heads/main fedcba9876543210\n")

	e, err := buildGitHookEvent("pre-push", repo, "", "", "", "origin", "git@example.com:repo.git", 2, stdin)
	if err != nil {
		t.Fatalf("buildGitHookEvent() error = %v", err)
	}
	if e.Type != event.EventTypePush {
		t.Fatalf("event type = %s, want %s", e.Type, event.EventTypePush)
	}
	if e.Labels["remote"] != "origin" {
		t.Fatalf("remote label = %q", e.Labels["remote"])
	}
	if e.Payload["phase"] != "pre_push" {
		t.Fatalf("phase payload = %#v", e.Payload["phase"])
	}
	refs, ok := e.Payload["refs"].([]map[string]string)
	if !ok || len(refs) != 1 {
		t.Fatalf("refs payload = %#v", e.Payload["refs"])
	}
	if refs[0]["local_sha"] != "0123456789ab" {
		t.Fatalf("local_sha = %q", refs[0]["local_sha"])
	}
}

func schemaHasFlag(schema commandSchema, name string) bool {
	for _, flag := range schema.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "OpenContext Test")
	return repo
}

func writeAndCommit(t *testing.T, repo, name, content, message string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", name)
	runGit(t, repo, "commit", "-m", message)
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
