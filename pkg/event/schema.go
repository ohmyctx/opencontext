package event

import (
	"fmt"
	"sync"
)

// FieldDef describes a single label or payload field for LLM context.
type FieldDef struct {
	Description string
	Example     string
}

// EventTypeSchema provides semantic documentation about an event type.
// The Memory Compiler includes relevant schemas in LLM summarization prompts
// so the model understands what each field means without guessing.
type EventTypeSchema struct {
	Source      Source
	Type        EventType
	Description string               // one-line description for LLM system prompt
	LabelDefs   map[string]FieldDef  // documentation for each label key
	PayloadDefs map[string]FieldDef  // documentation for each payload key
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*EventTypeSchema{}
)

func schemaKey(source Source, t EventType) string {
	return fmt.Sprintf("%s.%s", source, t)
}

// RegisterSchema adds or replaces a schema in the registry.
// Collectors call this in their init() for any custom event types.
func RegisterSchema(s *EventTypeSchema) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[schemaKey(s.Source, s.Type)] = s
}

// LookupSchema returns the schema for a source+type pair, or nil if not registered.
func LookupSchema(source Source, t EventType) *EventTypeSchema {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[schemaKey(source, t)]
}

// AllSchemas returns a copy of all registered schemas.
func AllSchemas() []*EventTypeSchema {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*EventTypeSchema, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	return out
}

// init registers schemas for all built-in event types.
func init() {
	builtins := []*EventTypeSchema{
		{
			Source:      SourceShell,
			Type:        EventTypeCommand,
			Description: "A shell command was executed by the user. exit_code=0 means success; non-zero indicates failure.",
			LabelDefs: map[string]FieldDef{
				"app":       {Description: "Shell application name", Example: "zsh"},
				"project":   {Description: "Project name inferred from git root or cwd basename", Example: "opencontext"},
				"cwd":       {Description: "Working directory when command executed", Example: "/root/code/opencontext"},
				"exit_code": {Description: "Exit code: 0=success, non-zero=error", Example: "1"},
			},
			PayloadDefs: map[string]FieldDef{
				"command":     {Description: "The command string that was executed", Example: "go build ./..."},
				"duration_ms": {Description: "Execution duration in milliseconds", Example: "423"},
				"user":        {Description: "Username who ran the command", Example: "root"},
			},
		},
		{
			Source:      SourceShell,
			Type:        EventTypeSessionEnd,
			Description: "A shell session ended (terminal tab/window closed).",
			LabelDefs: map[string]FieldDef{
				"app":     {Description: "Shell application name", Example: "zsh"},
				"project": {Description: "Last active project in this session", Example: "opencontext"},
			},
			PayloadDefs: map[string]FieldDef{
				"duration_ms":    {Description: "Total session duration in milliseconds", Example: "3600000"},
				"command_count":  {Description: "Number of commands run in this session", Example: "47"},
			},
		},
		{
			Source:      SourceGit,
			Type:        EventTypeCommit,
			Description: "A git commit was created. Indicates a meaningful unit of completed work.",
			LabelDefs: map[string]FieldDef{
				"repo":   {Description: "Repository name (dirname of git root)", Example: "opencontext"},
				"branch": {Description: "Branch the commit was made on", Example: "main"},
				"author": {Description: "Git author name", Example: "dev"},
			},
			PayloadDefs: map[string]FieldDef{
				"hash":          {Description: "Short commit hash", Example: "a1b2c3d"},
				"message":       {Description: "Commit message subject line", Example: "feat: implement HTTP ingester"},
				"files_changed": {Description: "Number of files changed", Example: "4"},
				"insertions":    {Description: "Lines added", Example: "182"},
				"deletions":     {Description: "Lines removed", Example: "12"},
			},
		},
		{
			Source:      SourceGit,
			Type:        EventTypeBranchSwitch,
			Description: "The user switched to a different git branch, indicating a context switch.",
			LabelDefs: map[string]FieldDef{
				"repo": {Description: "Repository name", Example: "opencontext"},
			},
			PayloadDefs: map[string]FieldDef{
				"from": {Description: "Branch switched from", Example: "feature/ingester"},
				"to":   {Description: "Branch switched to", Example: "main"},
			},
		},
		{
			Source:      SourceGit,
			Type:        EventTypePush,
			Description: "Code was pushed to a remote repository.",
			LabelDefs: map[string]FieldDef{
				"repo":   {Description: "Repository name", Example: "opencontext"},
				"branch": {Description: "Branch that was pushed", Example: "main"},
			},
			PayloadDefs: map[string]FieldDef{
				"remote":        {Description: "Remote name", Example: "origin"},
				"commit_count":  {Description: "Number of commits pushed", Example: "3"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeWindowFocus,
			Description: "User switched focus to a different application window.",
			LabelDefs: map[string]FieldDef{
				"app":   {Description: "Application name", Example: "cursor"},
				"class": {Description: "Window class/type", Example: "Code"},
			},
			PayloadDefs: map[string]FieldDef{
				"title":       {Description: "Window title, often contains filename and project", Example: "ingester.go - opencontext"},
				"duration_ms": {Description: "How long this window had focus before switching", Example: "1800000"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeAppLaunch,
			Description: "An application was launched.",
			LabelDefs: map[string]FieldDef{
				"app": {Description: "Application name", Example: "cursor"},
			},
			PayloadDefs: map[string]FieldDef{},
		},
		{
			Source:      SourceBrowser,
			Type:        EventTypePageVisit,
			Description: "User visited a web page. At L1 only the domain is recorded; full URL requires L2.",
			LabelDefs: map[string]FieldDef{
				"browser": {Description: "Browser name", Example: "chrome"},
				"domain":  {Description: "Website domain (L1)", Example: "pkg.go.dev"},
			},
			PayloadDefs: map[string]FieldDef{
				"title":       {Description: "Page title", Example: "modernc.org/sqlite - Go Packages"},
				"url":         {Description: "Full URL (L2 only)", Example: "https://pkg.go.dev/modernc.org/sqlite"},
				"duration_ms": {Description: "Time spent on this page in milliseconds", Example: "45000"},
			},
		},
		{
			Source:      SourceIDE,
			Type:        EventTypeFileSave,
			Description: "A file was saved in the IDE.",
			LabelDefs: map[string]FieldDef{
				"ide":      {Description: "IDE name", Example: "cursor"},
				"project":  {Description: "Project/workspace name", Example: "opencontext"},
				"language": {Description: "Programming language", Example: "go"},
			},
			PayloadDefs: map[string]FieldDef{
				"file":         {Description: "File path relative to project root", Example: "internal/ingester/handler.go"},
				"line_count":   {Description: "Total lines in file after save", Example: "142"},
			},
		},
	}

	for _, s := range builtins {
		RegisterSchema(s)
	}
}
