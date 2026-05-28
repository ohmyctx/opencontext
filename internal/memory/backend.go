// Package memory defines the MemoryBackend interface and provides a FileBackend
// implementation that writes a structured memory.md file.
package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohmyctx/opencontext/pkg/session"
)

// Backend writes compiled memory to a storage destination.
// Implementations: FileBackend (writes memory.md), future: Mem0Backend, etc.
type Backend interface {
	// Write persists the compiled memory content.
	Write(ctx context.Context, content *session.MemoryContent) error
}

// ── FileBackend ───────────────────────────────────────────────────────────────

// FileBackend writes memory as a Markdown file.
type FileBackend struct {
	path string
}

// NewFileBackend creates a FileBackend that writes to path.
// Parent directories are created on the first Write call.
func NewFileBackend(path string) *FileBackend {
	return &FileBackend{path: path}
}

// Write renders MemoryContent as Markdown and writes it to the configured path.
func (b *FileBackend) Write(ctx context.Context, content *session.MemoryContent) error {
	md := renderMarkdown(content)

	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return fmt.Errorf("create parent dirs for %s: %w", b.path, err)
	}

	if err := os.WriteFile(b.path, []byte(md), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", b.path, err)
	}
	return nil
}

// renderMarkdown converts MemoryContent to a memory.md string.
func renderMarkdown(c *session.MemoryContent) string {
	var sb strings.Builder

	updatedAt := time.UnixMilli(c.UpdatedAt).Format("2006-01-02 15:04")

	project := c.Project
	if project == "" || project == "_unknown" {
		project = "General"
	}

	sb.WriteString(fmt.Sprintf("# Project Memory: %s\n", project))
	sb.WriteString(fmt.Sprintf("> Updated: %s\n\n", updatedAt))

	if len(c.OpenLoops) > 0 {
		sb.WriteString("## Open Loops\n\n")
		for _, loop := range c.OpenLoops {
			sb.WriteString(fmt.Sprintf("- [ ] %s\n", loop))
		}
		sb.WriteString("\n")
	}

	if len(c.Hot) > 0 {
		sb.WriteString("## Today\n\n")
		for _, item := range c.Hot {
			writeMemoryItem(&sb, item)
		}
		sb.WriteString("\n")
	}

	if len(c.Warm) > 0 {
		sb.WriteString("## This Week\n\n")
		for _, item := range c.Warm {
			writeMemoryItem(&sb, item)
		}
		sb.WriteString("\n")
	}

	if len(c.Cold) > 0 {
		sb.WriteString("## History\n\n")
		for _, item := range c.Cold {
			writeMemoryItem(&sb, item)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func writeMemoryItem(sb *strings.Builder, item *session.MemoryItem) {
	if item.Title != "" {
		sb.WriteString(fmt.Sprintf("### %s\n\n", item.Title))
	}
	if item.Body != "" {
		body := strings.TrimRight(item.Body, "\n")
		sb.WriteString(body)
		sb.WriteString("\n")
	}
}
