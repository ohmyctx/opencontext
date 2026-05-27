// Package injector handles injecting OpenContext memory sections into
// third-party AI agent memory files (Hermes, OpenClaw, etc.).
//
// It maintains a clearly delimited section inside the target file using
// HTML comment markers so the agent's own memory is never overwritten:
//
//	<!-- opencontext:start -->
//	...generated content...
//	<!-- opencontext:end -->
//
// If the target file doesn't exist it is created. If the markers aren't
// present yet, the section is appended. On each update only the content
// between the markers is replaced.
package injector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	startMarker = "<!-- opencontext:start -->"
	endMarker   = "<!-- opencontext:end -->"
)

// InjectTarget describes a file to inject the memory section into.
type InjectTarget struct {
	// Path is the absolute path to the target memory file.
	Path string
	// Header is an optional markdown heading written inside the section.
	// Defaults to "## OpenContext — Recent Activity".
	Header string
}

// Inject writes content into the OpenContext section of target.Path.
// It creates the file (and parent directories) if needed.
// The rest of the file (the agent's own memory) is preserved.
func Inject(target InjectTarget, content string) error {
	path := expandHome(target.Path)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	header := target.Header
	if header == "" {
		header = "## OpenContext — Recent Activity"
	}

	section := startMarker + "\n" +
		header + "\n\n" +
		strings.TrimSpace(content) + "\n" +
		endMarker

	// Read existing file, or start empty.
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}

	updated := replaceSection(existing, section)

	return os.WriteFile(path, []byte(updated), 0o644)
}

// replaceSection replaces the content between the markers in existing,
// or appends the section if markers are not present.
func replaceSection(existing, section string) string {
	start := strings.Index(existing, startMarker)
	end := strings.Index(existing, endMarker)

	if start == -1 || end == -1 || end < start {
		// Markers not found — append the section.
		if existing == "" {
			return section + "\n"
		}
		// Ensure a blank line before the injected section.
		if !strings.HasSuffix(existing, "\n\n") {
			if !strings.HasSuffix(existing, "\n") {
				existing += "\n"
			}
			existing += "\n"
		}
		return existing + section + "\n"
	}

	// Replace between existing markers.
	before := existing[:start]
	after := existing[end+len(endMarker):]
	return before + section + after
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
