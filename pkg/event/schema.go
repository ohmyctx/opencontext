package event

import (
	"fmt"
	"sync"
)

// FieldDef describes a single label or payload field for LLM context.
type FieldDef struct {
	Description string `json:"description"`
	Example     string `json:"example"`
}

// EventTypeSchema provides semantic documentation about an event type.
// The Memory Compiler includes relevant schemas in LLM summarization prompts
// so the model understands what each field means without guessing.
type EventTypeSchema struct {
	Source      Source              `json:"source"`
	Type        EventType           `json:"type"`
	Description string              `json:"description"`  // one-line description for LLM system prompt
	LabelDefs   map[string]FieldDef `json:"label_defs"`   // documentation for each label key
	PayloadDefs map[string]FieldDef `json:"payload_defs"` // documentation for each payload key
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
				"duration_ms":   {Description: "Total session duration in milliseconds", Example: "3600000"},
				"command_count": {Description: "Number of commands run in this session", Example: "47"},
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
				"remote":       {Description: "Remote name", Example: "origin"},
				"commit_count": {Description: "Number of commits pushed", Example: "3"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeWindowFocus,
			Description: "User switched focus to a different application window.",
			LabelDefs: map[string]FieldDef{
				"app":      {Description: "Executable filename", Example: "chrome.exe"},
				"app_name": {Description: "Human-readable application name from Windows version info", Example: "Google Chrome"},
				"title":    {Description: "Window title, often contains filename and project", Example: "ingester.go - opencontext"},
				"class":    {Description: "Window class/type", Example: "Chrome_WidgetWin_1"},
			},
			PayloadDefs: map[string]FieldDef{
				"exe":           {Description: "Full executable path", Example: "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"},
				"pid":           {Description: "Process ID", Example: "12345"},
				"prev_app":      {Description: "Executable filename of the previously focused app", Example: "Weixin.exe"},
				"prev_app_name": {Description: "Human-readable name of the previously focused app", Example: "微信"},
				"duration_ms":   {Description: "How long this window had focus before switching", Example: "1800000"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeBrowserNav,
			Description: "User navigated to a new page in the browser (URL changed within the same window).",
			LabelDefs: map[string]FieldDef{
				"app":      {Description: "Browser executable name", Example: "chrome.exe"},
				"app_name": {Description: "Browser display name", Example: "Google Chrome"},
				"url":      {Description: "Full URL of the new page", Example: "https://github.com/yetanotherai/opencontext"},
				"title":    {Description: "Page title", Example: "opencontext/opencontext: GitHub"},
			},
			PayloadDefs: map[string]FieldDef{
				"url":      {Description: "Full URL of the new page", Example: "https://github.com/yetanotherai/opencontext"},
				"title":    {Description: "Page title", Example: "opencontext/opencontext: GitHub"},
				"prev_url": {Description: "URL of the previous page", Example: "https://github.com"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeClipboardCopy,
			Description: "User copied content to the clipboard. Reveals what the user is actively referencing or reusing across apps.",
			LabelDefs: map[string]FieldDef{
				"app":          {Description: "App that had focus when content was copied", Example: "chrome.exe"},
				"app_name":     {Description: "Human-readable app name", Example: "Google Chrome"},
				"content_type": {Description: "Clipboard content type: text, html, files, or image", Example: "text"},
				"file_count":   {Description: "Number of files copied (content_type=files only)", Example: "3"},
				"dimensions":   {Description: "Image dimensions (content_type=image only)", Example: "1920x1080"},
			},
			PayloadDefs: map[string]FieldDef{
				"text":       {Description: "Copied text (up to 500 chars; head+tail preview for longer content)", Example: "func handleRequest(w http.ResponseWriter..."},
				"text_len":   {Description: "Total character count of the original text", Example: "1420"},
				"truncated":  {Description: "True if content was cut due to length", Example: "true"},
				"files":      {Description: "List of copied file paths (up to 20)", Example: "[\"C:\\Users\\me\\doc.pdf\"]"},
				"file_count": {Description: "Total number of files copied", Example: "3"},
				"width":      {Description: "Image width in pixels", Example: "1920"},
				"height":     {Description: "Image height in pixels", Example: "1080"},
				"size_kb":    {Description: "Approximate image size in KB", Example: "512"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeUIClick,
			Description: "User clicked a UI control in an application window.",
			LabelDefs: map[string]FieldDef{
				"app":          {Description: "Executable filename of the active app", Example: "chrome.exe"},
				"app_name":     {Description: "Human-readable application name", Example: "Google Chrome"},
				"control_type": {Description: "UIA control type (e.g. ButtonControl, EditControl)", Example: "ButtonControl"},
				"control_name": {Description: "Accessible name of the clicked control", Example: "关闭"},
				"window_title": {Description: "Title of the window containing the clicked control", Example: "新标签页 - Google Chrome"},
			},
			PayloadDefs: map[string]FieldDef{
				"button":        {Description: "Mouse button: left, right, or middle", Example: "left"},
				"x":             {Description: "Screen X coordinate of the click", Example: "956"},
				"y":             {Description: "Screen Y coordinate of the click", Example: "540"},
				"class_name":    {Description: "Win32 window class of the clicked element", Example: "Chrome_RenderWidgetHostHWND"},
				"control_value": {Description: "Current text value of the clicked editable control (L2+)", Example: "search query"},
			},
		},
		{
			Source:      SourceOS,
			Type:        EventTypeAppLaunch,
			Description: "An application was launched.",
			LabelDefs: map[string]FieldDef{
				"app":      {Description: "Executable filename", Example: "chrome.exe"},
				"app_name": {Description: "Human-readable application name from Windows version info", Example: "Google Chrome"},
			},
			PayloadDefs: map[string]FieldDef{
				"exe":     {Description: "Full executable path", Example: "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"},
				"pid":     {Description: "Process ID at launch time", Example: "12345"},
				"cmdline": {Description: "First few tokens of the command line (never contains credentials)", Example: "chrome.exe --profile-directory=Default"},
			},
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
			Source:      SourceBrowser,
			Type:        EventTypeTabFocus,
			Description: "User focused a browser tab.",
			LabelDefs: map[string]FieldDef{
				"browser": {Description: "Browser name", Example: "chrome"},
				"domain":  {Description: "Website domain", Example: "github.com"},
			},
			PayloadDefs: map[string]FieldDef{
				"title":     {Description: "Page title", Example: "OpenContext Browser Collector"},
				"url":       {Description: "Full URL when sensitivity allows L2", Example: "https://github.com/yetanotherai/opencontext"},
				"tab_id":    {Description: "Browser tab identifier", Example: "123"},
				"window_id": {Description: "Browser window identifier", Example: "1"},
			},
		},
		{
			Source:      SourceBrowser,
			Type:        EventTypeLinkClick,
			Description: "User clicked a link in a browser page.",
			LabelDefs: map[string]FieldDef{
				"browser": {Description: "Browser name", Example: "chrome"},
				"domain":  {Description: "Website domain", Example: "developer.chrome.com"},
				"action":  {Description: "Action name", Example: "link_click"},
				"element": {Description: "HTML element tag", Example: "a"},
			},
			PayloadDefs: map[string]FieldDef{
				"title": {Description: "Current page title", Example: "Chrome Extensions documentation"},
				"url":   {Description: "Current page URL", Example: "https://developer.chrome.com/docs/extensions"},
				"text":  {Description: "Visible link text", Example: "Manifest file format"},
				"href":  {Description: "Link destination", Example: "https://developer.chrome.com/docs/extensions/reference/manifest"},
			},
		},
		{
			Source:      SourceBrowser,
			Type:        EventTypeButtonClick,
			Description: "User clicked a semantic button in a browser page.",
			LabelDefs: map[string]FieldDef{
				"browser": {Description: "Browser name", Example: "chrome"},
				"domain":  {Description: "Website domain", Example: "github.com"},
				"action":  {Description: "Action name", Example: "button_click"},
				"element": {Description: "HTML element tag", Example: "button"},
			},
			PayloadDefs: map[string]FieldDef{
				"title": {Description: "Current page title", Example: "Pull requests"},
				"url":   {Description: "Current page URL", Example: "https://github.com/yetanotherai/opencontext/pulls"},
				"text":  {Description: "Button label or accessible name", Example: "Create pull request"},
			},
		},
		{
			Source:      SourceBrowser,
			Type:        EventTypeSearch,
			Description: "User submitted a search query in a browser page.",
			LabelDefs: map[string]FieldDef{
				"browser":    {Description: "Browser name", Example: "chrome"},
				"domain":     {Description: "Website domain", Example: "google.com"},
				"action":     {Description: "Action name", Example: "search"},
				"input_type": {Description: "HTML input type", Example: "search"},
			},
			PayloadDefs: map[string]FieldDef{
				"title":       {Description: "Current page title", Example: "Google"},
				"url":         {Description: "Current page URL", Example: "https://www.google.com/search?q=opencontext"},
				"text":        {Description: "Search query", Example: "chrome extension manifest v3"},
				"text_len":    {Description: "Character count of the original query", Example: "31"},
				"field_name":  {Description: "Input name or id", Example: "q"},
				"placeholder": {Description: "Input placeholder", Example: "Search"},
			},
		},
		{
			Source:      SourceBrowser,
			Type:        EventTypeFormSubmit,
			Description: "User submitted a browser form. Raw field values are omitted unless captured as a separate text_input event.",
			LabelDefs: map[string]FieldDef{
				"browser": {Description: "Browser name", Example: "chrome"},
				"domain":  {Description: "Website domain", Example: "linear.app"},
				"action":  {Description: "Action name", Example: "form_submit"},
			},
			PayloadDefs: map[string]FieldDef{
				"title":       {Description: "Current page title", Example: "Create issue"},
				"url":         {Description: "Current page URL", Example: "https://linear.app/team/new"},
				"text":        {Description: "Summary of submitted text fields", Example: "2 text field(s) submitted"},
				"text_len":    {Description: "Total submitted text length", Example: "180"},
				"form_action": {Description: "Form action URL when available", Example: "https://example.com/search"},
			},
		},
		{
			Source:      SourceBrowser,
			Type:        EventTypeTextInput,
			Description: "User submitted text input content in a browser page. Disabled by default in the Chrome collector.",
			LabelDefs: map[string]FieldDef{
				"browser":    {Description: "Browser name", Example: "chrome"},
				"domain":     {Description: "Website domain", Example: "docs.google.com"},
				"action":     {Description: "Action name", Example: "text_input"},
				"input_type": {Description: "HTML input type", Example: "text"},
			},
			PayloadDefs: map[string]FieldDef{
				"title":       {Description: "Current page title", Example: "Document"},
				"url":         {Description: "Current page URL", Example: "https://docs.google.com/document/d/..."},
				"text":        {Description: "Submitted text, truncated by collector", Example: "refactor authentication flow"},
				"text_len":    {Description: "Original character count", Example: "28"},
				"field_name":  {Description: "Input name or id", Example: "title"},
				"placeholder": {Description: "Input placeholder", Example: "Untitled"},
				"truncated":   {Description: "True when text was truncated", Example: "false"},
			},
		},
		{
			Source:      SourceClaude,
			Type:        EventTypeUserMessage,
			Description: "A message sent by the user in a Claude Code session. Captures the user's intent and questions to the AI agent.",
			LabelDefs: map[string]FieldDef{
				"project":    {Description: "Project name inferred from session working directory", Example: "opencontext"},
				"session_id": {Description: "Claude Code session UUID", Example: "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb"},
			},
			PayloadDefs: map[string]FieldDef{
				"message":      {Description: "The text content of the user message", Example: "帮我实现 HTTP ingester"},
				"message_len":  {Description: "Character count of the message", Example: "42"},
				"session_file": {Description: "Absolute path to the JSONL session file", Example: "/root/.claude/projects/-root-code-opencontext/8478ea2f.jsonl"},
			},
		},
		{
			Source:      SourceClaude,
			Type:        EventTypeSessionStart,
			Description: "A new Claude Code session was started.",
			LabelDefs: map[string]FieldDef{
				"project":    {Description: "Project name inferred from session working directory", Example: "opencontext"},
				"session_id": {Description: "Claude Code session UUID", Example: "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb"},
			},
			PayloadDefs: map[string]FieldDef{},
		},
		{
			Source:      SourceCodex,
			Type:        EventTypeUserMessage,
			Description: "A message sent by the user in an OpenAI Codex CLI session.",
			LabelDefs: map[string]FieldDef{
				"project":    {Description: "Project name inferred from session working directory", Example: "opencontext"},
				"session_id": {Description: "Codex session UUID", Example: "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb"},
			},
			PayloadDefs: map[string]FieldDef{
				"message":     {Description: "The text content of the user message", Example: "Add error handling to the HTTP handler"},
				"message_len": {Description: "Character count of the message", Example: "42"},
				"model":       {Description: "Codex model used in this session", Example: "o4-mini"},
			},
		},
		{
			Source:      SourceCodex,
			Type:        EventTypeSessionStart,
			Description: "A new OpenAI Codex CLI session was started.",
			LabelDefs: map[string]FieldDef{
				"project":    {Description: "Project name inferred from session working directory", Example: "opencontext"},
				"session_id": {Description: "Codex session UUID", Example: "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb"},
			},
			PayloadDefs: map[string]FieldDef{
				"model": {Description: "Codex model used in this session", Example: "o4-mini"},
			},
		},
		{
			Source:      SourceCursor,
			Type:        EventTypeUserMessage,
			Description: "A prompt submitted by the user in the Cursor IDE agent.",
			LabelDefs: map[string]FieldDef{
				"project":         {Description: "Project name inferred from workspace root", Example: "opencontext"},
				"conversation_id": {Description: "Cursor conversation ID (stable across turns)", Example: "conv-8478ea2f"},
			},
			PayloadDefs: map[string]FieldDef{
				"message":     {Description: "The text content of the user prompt", Example: "Refactor the ingester to use channels"},
				"message_len": {Description: "Character count of the prompt", Example: "42"},
				"model":       {Description: "Model configured for this Cursor session", Example: "claude-sonnet-4-5"},
			},
		},
		{
			Source:      SourceCursor,
			Type:        EventTypeSessionStart,
			Description: "A new Cursor IDE agent session was started.",
			LabelDefs: map[string]FieldDef{
				"project":         {Description: "Project name inferred from workspace root", Example: "opencontext"},
				"conversation_id": {Description: "Cursor conversation ID", Example: "conv-8478ea2f"},
			},
			PayloadDefs: map[string]FieldDef{
				"model": {Description: "Model configured for this Cursor session", Example: "claude-sonnet-4-5"},
			},
		},
		{
			Source:      SourceOpenCode,
			Type:        EventTypeUserMessage,
			Description: "A message sent by the user in an OpenCode session.",
			LabelDefs: map[string]FieldDef{
				"project":    {Description: "Project name inferred from session working directory", Example: "opencontext"},
				"session_id": {Description: "OpenCode session ID", Example: "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb"},
			},
			PayloadDefs: map[string]FieldDef{
				"message":     {Description: "The text content of the user message", Example: "Add a REST endpoint for querying events"},
				"message_len": {Description: "Character count of the message", Example: "42"},
			},
		},
		{
			Source:      SourceOpenCode,
			Type:        EventTypeSessionStart,
			Description: "A new OpenCode session was started.",
			LabelDefs: map[string]FieldDef{
				"project":    {Description: "Project name inferred from session working directory", Example: "opencontext"},
				"session_id": {Description: "OpenCode session ID", Example: "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb"},
			},
			PayloadDefs: map[string]FieldDef{},
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
				"file":       {Description: "File path relative to project root", Example: "internal/ingester/handler.go"},
				"line_count": {Description: "Total lines in file after save", Example: "142"},
			},
		},
	}

	for _, s := range builtins {
		RegisterSchema(s)
	}
}
