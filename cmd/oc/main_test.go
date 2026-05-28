package main

import (
	"os"
	"path/filepath"
	"testing"
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

func schemaHasFlag(schema commandSchema, name string) bool {
	for _, flag := range schema.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}
