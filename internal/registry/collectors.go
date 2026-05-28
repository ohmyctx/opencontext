package registry

import (
	"sort"

	"github.com/yetanotherai/opencontext/pkg/event"
)

type CollectorKind string

const (
	KindBundledProcess CollectorKind = "bundled-process"
	KindBundledHook    CollectorKind = "bundled-hook"
	KindExternal       CollectorKind = "external"
)

type SchemaRef struct {
	Source string `json:"source"`
	Type   string `json:"type"`
}

type CollectorManifest struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Version     string        `json:"version"`
	Kind        CollectorKind `json:"kind"`
	Description string        `json:"description"`
	Platforms   []string      `json:"platforms"`
	Sources     []string      `json:"sources"`
	Install     []string      `json:"install"`
	Docs        string        `json:"docs,omitempty"`
	Schemas     []SchemaRef   `json:"schemas"`
}

func AllCollectors() []CollectorManifest {
	out := []CollectorManifest{
		{
			Name:        "shell",
			DisplayName: "Shell",
			Version:     "bundled",
			Kind:        KindBundledProcess,
			Description: "zsh/bash command activity collector installed as shell hooks.",
			Platforms:   []string{"darwin", "linux"},
			Sources:     []string{string(event.SourceShell)},
			Install:     []string{"oc collector shell install"},
		},
		{
			Name:        "claude",
			DisplayName: "Claude Code",
			Version:     "bundled",
			Kind:        KindBundledHook,
			Description: "Installs Claude Code HTTP hooks that post user prompts to the daemon hook adapter.",
			Platforms:   []string{"darwin", "linux", "windows"},
			Sources:     []string{string(event.SourceClaude)},
			Install:     []string{"oc collector claude install"},
		},
		{
			Name:        "codex",
			DisplayName: "Codex CLI",
			Version:     "bundled",
			Kind:        KindBundledHook,
			Description: "Installs Codex hook scripts that post user prompts to the daemon hook adapter.",
			Platforms:   []string{"darwin", "linux", "windows"},
			Sources:     []string{string(event.SourceCodex)},
			Install:     []string{"oc collector codex install"},
		},
		{
			Name:        "cursor",
			DisplayName: "Cursor",
			Version:     "bundled",
			Kind:        KindBundledHook,
			Description: "Installs Cursor hook scripts that post agent prompts to the daemon hook adapter.",
			Platforms:   []string{"darwin", "linux", "windows"},
			Sources:     []string{string(event.SourceCursor)},
			Install:     []string{"oc collector cursor install"},
		},
		{
			Name:        "opencode",
			DisplayName: "OpenCode",
			Version:     "bundled",
			Kind:        KindBundledHook,
			Description: "Installs OpenCode hook scripts that post user prompts to the daemon hook adapter.",
			Platforms:   []string{"darwin", "linux", "windows"},
			Sources:     []string{string(event.SourceOpenCode)},
			Install:     []string{"oc collector opencode install"},
		},
		{
			Name:        "browser-chrome",
			DisplayName: "Chrome Browser",
			Version:     "repo",
			Kind:        KindExternal,
			Description: "Chrome Manifest V3 extension for page visits, tab focus, searches, form submits, and explicit page actions.",
			Platforms:   []string{"darwin", "linux", "windows"},
			Sources:     []string{string(event.SourceBrowser)},
			Install:     []string{"see collectors/browser/README.md"},
			Docs:        "collectors/browser/README.md",
		},
		{
			Name:        "macos",
			DisplayName: "macOS Activity",
			Version:     "repo",
			Kind:        KindExternal,
			Description: "External collector for macOS app/window/click/text activity.",
			Platforms:   []string{"darwin"},
			Sources:     []string{string(event.SourceOS)},
			Install:     []string{"see docs/COLLECTOR_INSTALL.md"},
			Docs:        "docs/COLLECTOR_INSTALL.md#macos-activity-collector",
		},
		{
			Name:        "windows",
			DisplayName: "Windows Activity",
			Version:     "repo",
			Kind:        KindExternal,
			Description: "External collector for Windows app/window/click/text activity.",
			Platforms:   []string{"windows"},
			Sources:     []string{string(event.SourceOS)},
			Install:     []string{"see docs/COLLECTOR_INSTALL.md"},
			Docs:        "docs/COLLECTOR_INSTALL.md#windows-activity-collector",
		},
	}

	for i := range out {
		out[i].Schemas = schemasForSources(out[i].Sources)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func LookupCollector(name string) (CollectorManifest, bool) {
	for _, c := range AllCollectors() {
		if c.Name == name {
			return c, true
		}
	}
	return CollectorManifest{}, false
}

func schemasForSources(sources []string) []SchemaRef {
	allowed := map[string]bool{}
	for _, s := range sources {
		allowed[s] = true
	}
	refs := []SchemaRef{}
	for _, s := range event.AllSchemas() {
		if allowed[string(s.Source)] {
			refs = append(refs, SchemaRef{Source: string(s.Source), Type: string(s.Type)})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Source == refs[j].Source {
			return refs[i].Type < refs[j].Type
		}
		return refs[i].Source < refs[j].Source
	})
	return refs
}
