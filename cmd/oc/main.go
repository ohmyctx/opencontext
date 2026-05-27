// oc is the OpenContext CLI. It communicates with contextd over HTTP and also
// exposes collector subcommands used by shell hooks.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	claudecollector "github.com/yetanotherai/opencontext/collectors/claude"
	"github.com/yetanotherai/opencontext/pkg/client"
	"github.com/yetanotherai/opencontext/pkg/event"
)

var (
	daemonURL string
	jsonOut   bool
)

func main() {
	root := buildRoot()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "oc",
		Short: "OpenContext CLI — inspect events, trigger compiles, manage collectors",
		Long: `oc is the command-line interface for OpenContext.

Environment variables:
  OC_DAEMON_URL    contextd base URL (default: http://localhost:6060)`,
	}

	root.PersistentFlags().StringVar(&daemonURL, "daemon", envOrDefault("OC_DAEMON_URL", "http://localhost:6060"), "contextd base URL")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "output as JSON")

	root.AddCommand(
		buildStatusCmd(),
		buildEventsCmd(),
		buildCompileCmd(),
		buildCollectorCmd(),
		buildInjectCmd(),
	)

	return root
}

// ── oc status ─────────────────────────────────────────────────────────────────

func buildStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show contextd daemon health and statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			health, err := c.Health(ctx)
			if err != nil {
				return fmt.Errorf("contextd unreachable at %s: %w\n\nStart the daemon with: contextd", daemonURL, err)
			}

			if jsonOut {
				return printJSON(health)
			}

			fmt.Printf("contextd status: %s\n", health["status"])
			fmt.Printf("version:         %s\n", health["version"])
			fmt.Printf("uptime:          %ss\n", formatNum(health["uptime_seconds"]))
			fmt.Printf("events stored:   %s\n", formatNum(health["events_stored"]))
			fmt.Printf("daemon URL:      %s\n", daemonURL)
			return nil
		},
	}
}

// ── oc events ─────────────────────────────────────────────────────────────────

func buildEventsCmd() *cobra.Command {
	var (
		source  string
		project string
		since   string
		limit   int
		query   string
	)

	cmd := &cobra.Command{
		Use:   "events",
		Short: "List recent activity events",
		Example: `  oc events
  oc events --source shell --project opencontext --since 2h
  oc events --query "go build" --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			sinceMs := parseSinceDuration(since)

			q := &event.QueryRequest{
				Project: project,
				Since:   sinceMs,
				Limit:   limit,
				Query:   query,
			}
			if source != "" {
				q.Source = event.Source(source)
			}

			resp, err := c.QueryEvents(ctx, q)
			if err != nil {
				return fmt.Errorf("query events: %w", err)
			}

			if jsonOut {
				return printJSON(resp)
			}

			if len(resp.Events) == 0 {
				fmt.Println("No events found.")
				return nil
			}

			fmt.Printf("%-24s %-8s %-16s %s\n", "TIME", "SOURCE", "TYPE", "SUMMARY")
			fmt.Printf("%-24s %-8s %-16s %s\n", "────────────────────────", "────────", "────────────────", "───────────────────────────────────────")
			for _, e := range resp.Events {
				ts := time.UnixMilli(e.Ts).Format("2006-01-02 15:04:05")
				summary := buildEventSummary(e)
				fmt.Printf("%-24s %-8s %-16s %s\n", ts, e.Source, e.Type, summary)
			}

			if resp.Truncated {
				fmt.Printf("\n(showing %d of %d+, use --limit to see more)\n", len(resp.Events), resp.Total)
			} else {
				fmt.Printf("\n%d event(s)\n", resp.Total)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "filter by source (shell|git|os|browser|ide|im)")
	cmd.Flags().StringVar(&project, "project", "", "filter by project name")
	cmd.Flags().StringVar(&since, "since", "24h", "time window (e.g. 2h, 30m, 7d)")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum events to return")
	cmd.Flags().StringVar(&query, "query", "", "full-text search query")

	// oc events clear
	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete stored events",
		Example: `  oc events clear           # delete all events
  oc events clear --source shell  # delete shell events only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if source != "" {
				if err := c.DeleteEventsBySource(ctx, source); err != nil {
					return fmt.Errorf("delete %s events: %w", source, err)
				}
				fmt.Printf("Deleted all events with source: %s\n", source)
				return nil
			}

			if err := c.DeleteAllEvents(ctx); err != nil {
				return fmt.Errorf("delete events: %w", err)
			}
			fmt.Println("Deleted all events.")
			return nil
		},
	}
	clearCmd.Flags().StringVar(&source, "source", "", "delete events from a specific source (shell|git|os|browser|ide|im)")
	cmd.AddCommand(clearCmd)

	return cmd
}

// ── oc compile ────────────────────────────────────────────────────────────────

func buildCompileCmd() *cobra.Command {
	var subName string

	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Trigger memory compilation for a subscription",
		Example: `  oc compile
  oc compile --subscription opencontext-project`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := c.TriggerCompile(ctx, subName); err != nil {
				return fmt.Errorf("trigger compile: %w", err)
			}

			if subName == "" {
				fmt.Println("Memory compilation triggered for all subscriptions.")
			} else {
				fmt.Printf("Memory compilation triggered for subscription: %s\n", subName)
			}
			fmt.Println("(Compilation runs asynchronously — check memory.md in a moment)")
			return nil
		},
	}

	cmd.Flags().StringVar(&subName, "subscription", "", "subscription name (default: all)")
	return cmd
}

// ── oc collector ─────────────────────────────────────────────────────────────

func buildCollectorCmd() *cobra.Command {
	collector := &cobra.Command{
		Use:   "collector",
		Short: "Collector management subcommands",
	}
	collector.AddCommand(buildShellCollectorCmd())
	collector.AddCommand(buildClaudeCollectorCmd())
	collector.AddCommand(buildCodexCollectorCmd())
	collector.AddCommand(buildCursorCollectorCmd())
	collector.AddCommand(buildOpenCodeCollectorCmd())
	return collector
}

func buildShellCollectorCmd() *cobra.Command {
	shell := &cobra.Command{
		Use:   "shell",
		Short: "Shell collector commands",
	}
	shell.AddCommand(buildShellPushCmd())
	shell.AddCommand(buildShellInstallCmd())
	return shell
}

func buildShellPushCmd() *cobra.Command {
	var (
		command     string
		exitCode    int
		durationMs  int64
		cwd         string
		sensitivity int
	)

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push a shell command event to contextd",
		Long: `Push is called by shell hook scripts (zsh preexec/precmd) to record
a command execution event. It runs non-blocking and silently ignores
contextd being unavailable.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if command == "" {
				return nil // empty commands are silently dropped
			}

			project := detectProject(cwd)

			labels := map[string]string{
				"app":       detectShell(),
				"exit_code": strconv.Itoa(exitCode),
			}
			if cwd != "" {
				labels["cwd"] = cwd
			}
			if project != "" {
				labels["project"] = project
			}

			payload := map[string]any{
				"duration_ms": durationMs,
			}

			sens := event.SensitivityLevel(sensitivity)
			if sens == 0 {
				sens = event.SensitivityL1
			}

			// L1: command name (first word) only. L2: full string.
			if sens >= event.SensitivityL2 {
				payload["command"] = command
			} else {
				payload["command"] = firstWord(command)
			}

			e := &event.ActivityEvent{
				Ts:          time.Now().UnixMilli(),
				Source:      event.SourceShell,
				Type:        event.EventTypeCommand,
				Sensitivity: sens,
				Labels:      labels,
				Payload:     payload,
			}

			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// Non-blocking: silently ignore errors so the shell is never slowed down.
			_, _ = c.Push(ctx, e)
			return nil
		},
	}

	cmd.Flags().StringVar(&command, "command", "", "command string that was executed")
	cmd.Flags().IntVar(&exitCode, "exit-code", 0, "exit code of the command")
	cmd.Flags().Int64Var(&durationMs, "duration-ms", 0, "execution duration in milliseconds")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory when command ran")
	cmd.Flags().IntVar(&sensitivity, "sensitivity", 1, "sensitivity level (1=L1, 2=L2)")

	return cmd
}

func buildShellInstallCmd() *cobra.Command {
	var sensitivity int

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install shell hooks for zsh and bash",
		Long: `Install shell hooks that record commands to contextd.

Sensitivity levels:
  1 (L1) — command name only, e.g. "go" instead of "go build ./..."
  2 (L2, default) — full command string including arguments`,
		Example: `  oc collector shell install
  oc collector shell install --sensitivity 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installShellHooks(sensitivity)
		},
	}

	cmd.Flags().IntVar(&sensitivity, "sensitivity", 2, "sensitivity level: 1=command name only, 2=full command with args")
	return cmd
}

// ── claude collector ──────────────────────────────────────────────────────────

func buildClaudeCollectorCmd() *cobra.Command {
	claude := &cobra.Command{
		Use:   "claude",
		Short: "Claude Code session collector commands",
	}
	claude.AddCommand(buildClaudeStartCmd())
	claude.AddCommand(buildClaudeInstallCmd())
	return claude
}

func buildClaudeStartCmd() *cobra.Command {
	var (
		projectsDir string
		pollSecs    int
		sensitivity int
		verbose     bool
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start watching Claude Code sessions and push user messages to contextd",
		Long: `Watches ~/.claude/projects/**/*.jsonl for new user messages and pushes
them as events to contextd. Runs in the foreground; use 'install' to
auto-start on shell launch.

Sensitivity levels:
  1 (L1) — message length only (no message text stored)
  2 (L2, default) — full message text stored`,
		Example: `  oc collector claude start
  oc collector claude start --sensitivity 2 --poll 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logLevel := slog.LevelInfo
			if verbose {
				logLevel = slog.LevelDebug
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

			cfg := claudecollector.DefaultConfig()
			cfg.DaemonURL = daemonURL
			cfg.Sensitivity = event.SensitivityLevel(sensitivity)
			cfg.PollInterval = time.Duration(pollSecs) * time.Second
			if projectsDir != "" {
				cfg.ClaudeProjectsDir = projectsDir
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return claudecollector.New(cfg, log).Run(ctx)
		},
	}

	cmd.Flags().StringVar(&projectsDir, "projects-dir", "", "Claude Code projects directory (default: ~/.claude/projects)")
	cmd.Flags().IntVar(&pollSecs, "poll", 3, "poll interval in seconds")
	cmd.Flags().IntVar(&sensitivity, "sensitivity", 2, "sensitivity level: 1=length only, 2=full message text")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "verbose debug logging")

	return cmd
}

func buildClaudeInstallCmd() *cobra.Command {
	var sensitivity int

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Auto-start Claude Code collector on shell launch",
		Long: `Adds a background launch of 'oc collector claude start' to ~/.zshrc.
Uses a PID file to prevent duplicate processes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installClaudeHooks(sensitivity)
		},
	}

	cmd.Flags().IntVar(&sensitivity, "sensitivity", 2, "sensitivity level: 1=length only, 2=full message text")
	return cmd
}

func installClaudeHooks(sensitivity int) error {
	ocBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve oc binary path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(ocBin); err == nil {
		ocBin = resolved
	}

	home, _ := os.UserHomeDir()
	pidFile := filepath.Join(home, ".opencontext", "collectors", "claude", "collector.pid")

	// Shell snippet: start the collector only if not already running.
	snippet := fmt.Sprintf(`
# OpenContext — Claude Code collector (auto-start)
_oc_claude_start() {
  local pidfile=%s
  if [[ -f "$pidfile" ]]; then
    local pid
    pid=$(cat "$pidfile")
    kill -0 "$pid" 2>/dev/null && return  # already running
  fi
  mkdir -p "$(dirname "$pidfile")"
  %s collector claude start --sensitivity %d &>/dev/null &
  echo $! > "$pidfile"
}
_oc_claude_start
`, pidFile, ocBin, sensitivity)

	zshrc := filepath.Join(home, ".zshrc")
	appendIfMissing(zshrc, snippet, "oc_claude_start")

	fmt.Println("Claude Code collector auto-start installed.")
	fmt.Printf("  sensitivity: L%d\n", sensitivity)
	fmt.Printf("  pid file:    %s\n", pidFile)
	fmt.Println("\nRestart your shell or run:")
	fmt.Printf("  %s collector claude start --sensitivity %d &\n", ocBin, sensitivity)
	return nil
}

// ── Codex CLI collector ───────────────────────────────────────────────────────

func buildCodexCollectorCmd() *cobra.Command {
	codex := &cobra.Command{
		Use:   "codex",
		Short: "OpenAI Codex CLI hook collector commands",
	}
	codex.AddCommand(buildCodexInstallCmd())
	return codex
}

func buildCodexInstallCmd() *cobra.Command {
	var daemonAddr string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext hooks into Codex CLI (~/.codex/config.json)",
		Long: `Adds UserPromptSubmit and SessionStart HTTP hooks to Codex CLI.
Codex will POST each user message to contextd for recording.

Requires Codex CLI with hooks support (codex >= 0.1.x).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installCodexHooks(daemonAddr)
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "contextd base URL")
	return cmd
}

func installCodexHooks(daemonAddr string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	hooksDir := filepath.Join(home, ".opencontext", "collectors", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	// Shell adapter script: reads JSON from stdin, POSTs to contextd async.
	scriptPath := filepath.Join(hooksDir, "codex.sh")
	script := fmt.Sprintf(`#!/usr/bin/env bash
# OpenContext hook adapter for Codex CLI — auto-generated by: oc collector codex install
INPUT=$(cat)
curl -sf -X POST %s/api/v1/hooks/codex \
  -H "Content-Type: application/json" \
  --data-raw "$INPUT" >/dev/null 2>&1 &
exit 0
`, daemonAddr)

	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write hook script: %w", err)
	}

	// Codex CLI hooks: ~/.codex/hooks.json (top-level event keys, no "hooks" wrapper)
	codexCfgDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexCfgDir, 0o755); err != nil {
		return err
	}
	codexHooksPath := filepath.Join(codexCfgDir, "hooks.json")

	cfgJSON, err := patchCodexHooks(codexHooksPath, scriptPath)
	if err != nil {
		return fmt.Errorf("patch codex hooks: %w", err)
	}
	if err := os.WriteFile(codexHooksPath, cfgJSON, 0o644); err != nil {
		return fmt.Errorf("write codex hooks: %w", err)
	}

	fmt.Println("Codex CLI hooks installed.")
	fmt.Printf("  hook script:  %s\n", scriptPath)
	fmt.Printf("  codex hooks:  %s\n", codexHooksPath)
	fmt.Printf("  endpoint:     %s/api/v1/hooks/codex\n", daemonAddr)
	fmt.Println("\nStart contextd, then open a Codex session. Messages will be recorded.")
	fmt.Println("Note: Codex may prompt you to trust the hook on first run (/hooks to review).")
	return nil
}

// patchCodexHooks reads (or creates) ~/.codex/hooks.json.
// Codex hooks.json has events as TOP-LEVEL keys (no "hooks" wrapper).
// Each event maps to an array of matcher-group objects: [{matcher?, hooks: [{type, command}]}]
func patchCodexHooks(cfgPath, scriptPath string) ([]byte, error) {
	// Read existing hooks.json or start fresh.
	existing := map[string]json.RawMessage{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	// Each entry: {"hooks": [{"type": "command", "command": "/path/to/script"}]}
	ocEntry := map[string]any{
		"hooks": []map[string]string{{"type": "command", "command": scriptPath}},
	}
	entryJSON, _ := json.Marshal(ocEntry)

	for _, eventName := range []string{"UserPromptSubmit", "SessionStart"} {
		existing[eventName] = prependJSONEntry(existing[eventName], entryJSON, scriptPath)
	}

	return json.MarshalIndent(existing, "", "  ")
}

// prependJSONEntry removes array entries containing scriptPath and prepends a fresh entry.
func prependJSONEntry(existing json.RawMessage, entry json.RawMessage, dedupKey string) json.RawMessage {
	var items []json.RawMessage
	if existing != nil {
		_ = json.Unmarshal(existing, &items)
	}
	filtered := []json.RawMessage{entry}
	for _, item := range items {
		if !containsStr(string(item), dedupKey) {
			filtered = append(filtered, item)
		}
	}
	out, _ := json.Marshal(filtered)
	return out
}

// ── Cursor IDE collector ──────────────────────────────────────────────────────

func buildCursorCollectorCmd() *cobra.Command {
	cursor := &cobra.Command{
		Use:   "cursor",
		Short: "Cursor IDE agent hook collector commands",
	}
	cursor.AddCommand(buildCursorInstallCmd())
	return cursor
}

func buildCursorInstallCmd() *cobra.Command {
	var daemonAddr string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext hooks into Cursor IDE (~/.cursor/hooks.json)",
		Long: `Adds beforeSubmitPrompt and sessionStart command hooks to Cursor IDE.
Cursor will execute the hook script on each user prompt submission.

Requires Cursor IDE with hooks support (Cursor >= 1.0).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installCursorHooks(daemonAddr)
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "contextd base URL")
	return cmd
}

func installCursorHooks(daemonAddr string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Create hook script in ~/.cursor/hooks/ (Cursor user hook working dir)
	cursorHooksDir := filepath.Join(home, ".cursor", "hooks")
	if err := os.MkdirAll(cursorHooksDir, 0o755); err != nil {
		return err
	}

	scriptPath := filepath.Join(cursorHooksDir, "oc-capture.sh")
	script := fmt.Sprintf(`#!/usr/bin/env bash
# OpenContext hook adapter for Cursor IDE — auto-generated by: oc collector cursor install
INPUT=$(cat)
curl -sf -X POST %s/api/v1/hooks/cursor \
  -H "Content-Type: application/json" \
  --data-raw "$INPUT" >/dev/null 2>&1 &
exit 0
`, daemonAddr)

	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write hook script: %w", err)
	}

	// Patch ~/.cursor/hooks.json
	cursorCfgPath := filepath.Join(home, ".cursor", "hooks.json")
	cfgJSON, err := patchCursorHooks(cursorCfgPath)
	if err != nil {
		return fmt.Errorf("patch cursor hooks: %w", err)
	}
	if err := os.WriteFile(cursorCfgPath, cfgJSON, 0o644); err != nil {
		return fmt.Errorf("write cursor hooks: %w", err)
	}

	fmt.Println("Cursor IDE hooks installed.")
	fmt.Printf("  hook script:    %s\n", scriptPath)
	fmt.Printf("  cursor hooks:   %s\n", cursorCfgPath)
	fmt.Printf("  endpoint:       %s/api/v1/hooks/cursor\n", daemonAddr)
	fmt.Println("\nReload Cursor. Agent prompts and session starts will be recorded.")
	return nil
}

// patchCursorHooks reads (or creates) ~/.cursor/hooks.json and adds our entries.
// Cursor hooks.json format: {"version": 1, "hooks": {"beforeSubmitPrompt": [...], ...}}
// The "hooks" sub-object wraps all hook event arrays.
// User hook scripts run from ~/.cursor/, so we use a relative path.
func patchCursorHooks(cfgPath string) ([]byte, error) {
	// Relative path from ~/.cursor/: Cursor user hooks CWD is ~/.cursor/
	ocEntry := map[string]string{"command": "./hooks/oc-capture.sh"}
	entryJSON, _ := json.Marshal(ocEntry)

	// Parse the full file preserving unknown top-level keys.
	topLevel := map[string]json.RawMessage{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(data, &topLevel)
	}

	topLevel["version"] = json.RawMessage(`1`)

	// Remove any hook event keys that may have been written at the top level
	// by older versions of the installer (events belong inside "hooks" sub-object).
	for _, key := range []string{"beforeSubmitPrompt", "sessionStart"} {
		delete(topLevel, key)
	}

	// Decode the "hooks" sub-object (or start fresh).
	hooksMap := map[string]json.RawMessage{}
	if raw, ok := topLevel["hooks"]; ok {
		_ = json.Unmarshal(raw, &hooksMap)
	}

	for _, key := range []string{"beforeSubmitPrompt", "sessionStart"} {
		hooksMap[key] = prependJSONEntry(hooksMap[key], entryJSON, "oc-capture")
	}

	hooksRaw, err := json.Marshal(hooksMap)
	if err != nil {
		return nil, err
	}
	topLevel["hooks"] = hooksRaw

	return json.MarshalIndent(topLevel, "", "  ")
}

// ── OpenCode collector ────────────────────────────────────────────────────────

func buildOpenCodeCollectorCmd() *cobra.Command {
	opencode := &cobra.Command{
		Use:   "opencode",
		Short: "OpenCode (sst/opencode) hook collector commands",
	}
	opencode.AddCommand(buildOpenCodeInstallCmd())
	return opencode
}

func buildOpenCodeInstallCmd() *cobra.Command {
	var daemonAddr string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext hooks into OpenCode (~/.config/opencode/hooks.json)",
		Long: `Adds UserPromptSubmit and SessionStart command hooks to OpenCode.
OpenCode will execute the hook script on each user message submission.

Supports both the native opencode hook format and the Claude-compatible
format (via opencode-claude-hooks npm package).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installOpenCodeHooks(daemonAddr)
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "contextd base URL")
	return cmd
}

func installOpenCodeHooks(daemonAddr string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	hooksDir := filepath.Join(home, ".opencontext", "collectors", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	scriptPath := filepath.Join(hooksDir, "opencode.sh")
	script := fmt.Sprintf(`#!/usr/bin/env bash
# OpenContext hook adapter for OpenCode — auto-generated by: oc collector opencode install
INPUT=$(cat)
curl -sf -X POST %s/api/v1/hooks/opencode \
  -H "Content-Type: application/json" \
  --data-raw "$INPUT" >/dev/null 2>&1 &
exit 0
`, daemonAddr)

	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write hook script: %w", err)
	}

	// OpenCode config: ~/.config/opencode/hooks.json
	opencodeCfgDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(opencodeCfgDir, 0o755); err != nil {
		return err
	}
	opencodeCfgPath := filepath.Join(opencodeCfgDir, "hooks.json")
	cfgJSON, err := patchOpenCodeHooks(opencodeCfgPath, scriptPath)
	if err != nil {
		return fmt.Errorf("patch opencode hooks: %w", err)
	}
	if err := os.WriteFile(opencodeCfgPath, cfgJSON, 0o644); err != nil {
		return fmt.Errorf("write opencode hooks: %w", err)
	}

	fmt.Println("OpenCode hooks installed.")
	fmt.Printf("  hook script:     %s\n", scriptPath)
	fmt.Printf("  opencode hooks:  %s\n", opencodeCfgPath)
	fmt.Printf("  endpoint:        %s/api/v1/hooks/opencode\n", daemonAddr)
	fmt.Println("\nStart or restart OpenCode. User messages will be recorded.")
	fmt.Println()
	fmt.Println("Note: OpenCode hook support may vary by version.")
	fmt.Println("If using opencode-claude-hooks npm package, you can also")
	fmt.Println("point it at ~/.claude/settings.json (Claude hooks are compatible).")
	return nil
}

// patchOpenCodeHooks reads (or creates) ~/.config/opencode/hooks.json.
// OpenCode uses a Claude-compatible hook format.
func patchOpenCodeHooks(cfgPath, scriptPath string) ([]byte, error) {
	ocEntry := map[string]any{
		"hooks": []map[string]string{{"type": "command", "command": scriptPath}},
	}
	entryJSON, _ := json.Marshal(ocEntry)

	existing := map[string]json.RawMessage{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	for _, eventName := range []string{"UserPromptSubmit", "SessionStart"} {
		existing[eventName] = prependJSONEntry(existing[eventName], entryJSON, scriptPath)
	}

	return json.MarshalIndent(existing, "", "  ")
}

// ── shell helpers ─────────────────────────────────────────────────────────────

func installShellHooks(sensitivity int) error {
	if sensitivity < 1 || sensitivity > 2 {
		sensitivity = 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Resolve the absolute path of the oc binary so the hook works regardless
	// of whether oc is in PATH.
	ocBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve oc binary path: %w", err)
	}
	// Follow symlinks so the stored path is the real binary.
	if resolved, err := filepath.EvalSymlinks(ocBin); err == nil {
		ocBin = resolved
	}

	hooksDir := home + "/.opencontext/collectors/shell"
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	zshHook := fmt.Sprintf(`# OpenContext shell hooks — installed by: oc collector shell install
# Re-run install to update.
# oc binary: %s  sensitivity: %d

_oc_preexec() {
  _oc_cmd_start=$(date +%%s%%3N)
  _oc_cmd_input=$1
}

# Commands that are always skipped (matches server-side DefaultConfig filter).
# Avoids sending an HTTP request that will be dropped immediately.
_oc_skip_cmd() {
  local cmd=$1
  # Leading space = shell privacy convention
  [[ "$cmd" == " "* ]] && return 0
  # Extract first word (%%%% in fmt.Sprintf → %% in shell → longest-suffix strip)
  local first="${cmd%%%% *}"
  # Bare no-arg meta/navigation commands
  case "$first" in
    clear|reset|ls|ll|la|pwd|cd|history|exit)
      [[ "$cmd" == "$first" ]] && return 0 ;;
  esac
  return 1
}

_oc_precmd() {
  local _oc_exit=$?
  local _oc_end
  _oc_end=$(date +%%s%%3N)
  local _oc_dur=$(( _oc_end - ${_oc_cmd_start:-$_oc_end} ))

  [[ -z "$_oc_cmd_input" ]] && return 0
  _oc_skip_cmd "$_oc_cmd_input" && { _oc_cmd_input=""; return 0; }

  # &! runs in background and immediately disowns the job so zsh never
  # prints the "[1] + done ..." completion notification.
  %s collector shell push \
    --command "$_oc_cmd_input" \
    --exit-code "$_oc_exit" \
    --duration-ms "$_oc_dur" \
    --cwd "$PWD" \
    --sensitivity %d &>/dev/null &!

  _oc_cmd_input=""
}

autoload -Uz add-zsh-hook
add-zsh-hook preexec _oc_preexec
add-zsh-hook precmd _oc_precmd
`, ocBin, sensitivity, ocBin, sensitivity)

	bashHook := fmt.Sprintf(`# OpenContext shell hooks — installed by: oc collector shell install
# oc binary: %s  sensitivity: %d

_oc_preexec() {
  _oc_cmd_start=$(date +%%s%%3N 2>/dev/null || echo 0)
  _oc_cmd_input=$BASH_COMMAND
}

_oc_skip_cmd() {
  local cmd=$1
  [[ "$cmd" == " "* ]] && return 0
  local first="${cmd%%%% *}"
  case "$first" in
    clear|reset|ls|ll|la|pwd|cd|history|exit)
      [[ "$cmd" == "$first" ]] && return 0 ;;
  esac
  return 1
}

_oc_precmd() {
  local _oc_exit=$?
  local _oc_end
  _oc_end=$(date +%%s%%3N 2>/dev/null || echo 0)
  local _oc_dur=$(( _oc_end - ${_oc_cmd_start:-0} ))

  [[ -z "$_oc_cmd_input" ]] && return 0
  [[ "$_oc_cmd_input" == "_oc_precmd" ]] && return 0
  _oc_skip_cmd "$_oc_cmd_input" && { _oc_cmd_input=""; return 0; }

  # Wrap in subshell so bash doesn't track the job and print notifications.
  ( %s collector shell push \
    --command "$_oc_cmd_input" \
    --exit-code "$_oc_exit" \
    --duration-ms "$_oc_dur" \
    --cwd "$PWD" \
    --sensitivity %d &>/dev/null 2>&1 & )

  _oc_cmd_input=""
}

trap '_oc_preexec "$BASH_COMMAND"' DEBUG
PROMPT_COMMAND="_oc_precmd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
`, ocBin, sensitivity, ocBin, sensitivity)

	if err := os.WriteFile(hooksDir+"/hooks.zsh", []byte(zshHook), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(hooksDir+"/hooks.bash", []byte(bashHook), 0o644); err != nil {
		return err
	}

	zshrc := home + "/.zshrc"
	bashrc := home + "/.bashrc"

	sourceLine := "\n# OpenContext shell collector\nsource ~/.opencontext/collectors/shell/hooks.zsh\n"
	appendIfMissing(zshrc, sourceLine, "hooks.zsh")

	sourceLine = "\n# OpenContext shell collector\nsource ~/.opencontext/collectors/shell/hooks.bash\n"
	appendIfMissing(bashrc, sourceLine, "hooks.bash")

	sensLabel := "L2 (full command with args)"
	if sensitivity == 1 {
		sensLabel = "L1 (command name only)"
	}

	fmt.Println("Shell hooks installed.")
	fmt.Printf("  sensitivity: %s\n", sensLabel)
	fmt.Printf("  zsh:  %s/hooks.zsh  (added to ~/.zshrc)\n", hooksDir)
	fmt.Printf("  bash: %s/hooks.bash (added to ~/.bashrc)\n", hooksDir)
	fmt.Println("\nRestart your shell or run: source ~/.zshrc")
	fmt.Println("To change sensitivity, re-run: oc collector shell install --sensitivity 2")
	return nil
}

func appendIfMissing(path, content, marker string) {
	data, _ := os.ReadFile(path)
	if containsStr(string(data), marker) {
		return // already installed
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(content)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(s) > 0 && (s[:len(sub)] == sub || containsStr(s[1:], sub)))
}

func detectProject(cwd string) string {
	if cwd == "" {
		return ""
	}
	// Walk up looking for .git
	dir := cwd
	for {
		if _, err := os.Stat(dir + "/.git"); err == nil {
			// Found git root — use directory basename
			for i := len(dir) - 1; i >= 0; i-- {
				if dir[i] == '/' {
					return dir[i+1:]
				}
			}
			return dir
		}
		parent := ""
		for i := len(dir) - 1; i >= 0; i-- {
			if dir[i] == '/' {
				parent = dir[:i]
				break
			}
		}
		if parent == "" || parent == dir {
			break
		}
		dir = parent
	}
	// Fall back to cwd basename
	for i := len(cwd) - 1; i >= 0; i-- {
		if cwd[i] == '/' {
			return cwd[i+1:]
		}
	}
	return cwd
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	for i := len(shell) - 1; i >= 0; i-- {
		if shell[i] == '/' {
			return shell[i+1:]
		}
	}
	if shell != "" {
		return shell
	}
	return "sh"
}

func firstWord(s string) string {
	for i, c := range s {
		if c == ' ' || c == '\t' {
			return s[:i]
		}
	}
	return s
}

// ── oc inject ─────────────────────────────────────────────────────────────────

func buildInjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inject",
		Short: "Inject OpenContext memory into third-party AI agent files",
		Long: `Adds an inject_targets entry to your OpenContext subscription config
so that memory.md is automatically pushed into the target agent's
memory file (Hermes MEMORY.md, OpenClaw MEMORY.md, etc.) on every
refresh cycle.

The injected block is wrapped in HTML comment markers so the agent's
own memory is never overwritten:

  <!-- opencontext:start -->
  ## OpenContext — Recent Activity
  ...generated content...
  <!-- opencontext:end -->`,
	}
	cmd.AddCommand(buildInjectHermesCmd())
	cmd.AddCommand(buildInjectOpenClawCmd())
	return cmd
}

func buildInjectHermesCmd() *cobra.Command {
	var (
		memoryPath string
		header     string
		configFile string
	)

	cmd := &cobra.Command{
		Use:   "hermes",
		Short: "Inject memory into Hermes Agent (~/.hermes/memories/MEMORY.md)",
		Long: `Adds Hermes's MEMORY.md as an inject_target in your OpenContext
subscription config. After the next refresh cycle, OpenContext will
maintain an "OpenContext — Recent Activity" section in that file.

Hermes also reads .hermes.md / AGENTS.md / CLAUDE.md from the project
directory — those files are already populated if you have a project
subscription with claude_md configured.`,
		Example: `  oc inject hermes
  oc inject hermes --memory ~/.hermes/memories/MEMORY.md
  oc inject hermes --header "## Recent Dev Activity"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installInjectTarget("hermes", memoryPath, header, configFile)
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&memoryPath, "memory", filepath.Join(home, ".hermes", "memories", "MEMORY.md"), "path to Hermes MEMORY.md")
	cmd.Flags().StringVar(&header, "header", "## OpenContext — Recent Activity", "section heading inside the injected block")
	cmd.Flags().StringVar(&configFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	return cmd
}

func buildInjectOpenClawCmd() *cobra.Command {
	var (
		memoryPath string
		header     string
		configFile string
	)

	cmd := &cobra.Command{
		Use:   "openclaw",
		Short: "Inject memory into OpenClaw workspace (~/.openclaw/workspace/MEMORY.md)",
		Long: `Adds OpenClaw's workspace MEMORY.md as an inject_target in your
OpenContext subscription config. After the next refresh cycle,
OpenContext will maintain an "OpenContext — Recent Activity" section
in that file.

If your OpenClaw agents use a custom workspace path, pass it with --memory.`,
		Example: `  oc inject openclaw
  oc inject openclaw --memory ~/.openclaw/my-agent/MEMORY.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installInjectTarget("openclaw", memoryPath, header, configFile)
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&memoryPath, "memory", filepath.Join(home, ".openclaw", "workspace", "MEMORY.md"), "path to OpenClaw MEMORY.md")
	cmd.Flags().StringVar(&header, "header", "## OpenContext — Recent Activity", "section heading inside the injected block")
	cmd.Flags().StringVar(&configFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	return cmd
}

// installInjectTarget patches the first raw_dump subscription in config.yaml
// to add the given path as an inject_target, then writes the file back.
func installInjectTarget(tool, memoryPath, header, configFile string) error {
	if configFile == "" {
		home, _ := os.UserHomeDir()
		configFile = filepath.Join(home, ".opencontext", "config.yaml")
	}

	// Read the raw YAML so we can do a targeted append without losing formatting.
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("read config %s: %w\n\nRun 'contextd' first to create the default config.", configFile, err)
	}

	content := string(data)

	// Check if this target is already registered.
	if containsStr(content, memoryPath) {
		fmt.Printf("%s inject target already registered: %s\n", tool, memoryPath)
		return nil
	}

	// Build the YAML snippet to inject.
	// We append under the first subscription's memory block.
	// If inject_targets already exists we add a new entry; otherwise we add the block.
	snippet := fmt.Sprintf("        - path: %s\n          header: \"%s\"\n", memoryPath, header)

	if containsStr(content, "inject_targets:") {
		// inject_targets block already exists — append our entry after the last one.
		idx := strings.LastIndex(content, "inject_targets:")
		insertAt := strings.Index(content[idx:], "\n")
		if insertAt == -1 {
			content += "\n" + snippet
		} else {
			// Find the end of the inject_targets block (next key at same indentation level).
			blockStart := idx + insertAt + 1
			// Append before next top-level memory key.
			content = content[:blockStart] + snippet + content[blockStart:]
		}
	} else {
		// No inject_targets yet — add the block after the first `memory:` occurrence.
		memIdx := strings.Index(content, "    memory:")
		if memIdx == -1 {
			return fmt.Errorf("could not find 'memory:' block in %s\n\nAdd inject_targets manually — see docs/COLLECTORS.md", configFile)
		}
		// Find end of memory block's first line.
		lineEnd := strings.Index(content[memIdx:], "\n")
		if lineEnd == -1 {
			content += "\n      inject_targets:\n" + snippet
		} else {
			insertAt := memIdx + lineEnd + 1
			content = content[:insertAt] +
				"      inject_targets:\n" + snippet +
				content[insertAt:]
		}
	}

	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("%s inject target installed.\n", tool)
	fmt.Printf("  target file: %s\n", memoryPath)
	fmt.Printf("  config:      %s\n", configFile)
	fmt.Println("\nRestart contextd (or run: make restart) for changes to take effect.")
	fmt.Println("The memory section will be injected on the next refresh cycle.")
	return nil
}

// ── output helpers ────────────────────────────────────────────────────────────

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func buildEventSummary(e *event.ActivityEvent) string {
	switch e.Source {
	case event.SourceShell:
		cmd, _ := e.Payload["command"].(string)
		exit := e.Labels["exit_code"]
		proj := e.Labels["project"]
		s := cmd
		if proj != "" {
			s = "[" + proj + "] " + s
		}
		if exit != "" && exit != "0" {
			s += "  (exit " + exit + ")"
		}
		return s
	case event.SourceGit:
		msg, _ := e.Payload["message"].(string)
		branch := e.Labels["branch"]
		if msg != "" && branch != "" {
			return branch + ": " + msg
		}
		return msg + branch
	case event.SourceClaude, event.SourceCodex, event.SourceCursor, event.SourceOpenCode:
		msg, _ := e.Payload["message"].(string)
		proj := e.Labels["project"]
		if msg == "" {
			msgLen, _ := e.Payload["message_len"].(float64)
			msg = fmt.Sprintf("(message, %d chars)", int(msgLen))
		}
		if len([]rune(msg)) > 60 {
			runes := []rune(msg)
			msg = string(runes[:57]) + "..."
		}
		if proj != "" {
			return "[" + proj + "] " + msg
		}
		return msg
	case event.SourceBrowser:
		domain := e.Labels["domain"]
		title, _ := e.Payload["title"].(string)
		if title != "" {
			return domain + " — " + title
		}
		return domain
	case event.SourceOS:
		switch e.Type {
		case event.EventTypeBrowserNav:
			url := e.Labels["url"]
			title := e.Labels["title"]
			appName := e.Labels["app_name"]
			// Strip protocol for brevity
			displayURL := url
			for _, pfx := range []string{"https://", "http://"} {
				if len(url) > len(pfx) && url[:len(pfx)] == pfx {
					displayURL = url[len(pfx):]
					break
				}
			}
			if title != "" && displayURL != "" {
				return title + "  (" + displayURL + ")"
			}
			if displayURL != "" {
				return displayURL
			}
			if appName != "" {
				return appName
			}
			return e.Labels["app"]
		case event.EventTypeWindowFocus:
			appName := e.Labels["app_name"]
			title := e.Labels["title"]
			app := e.Labels["app"]
			url := e.Labels["url"]
			// For browsers, show URL (stripped) instead of just window title
			if url != "" {
				displayURL := url
				for _, pfx := range []string{"https://", "http://"} {
					if len(url) > len(pfx) && url[:len(pfx)] == pfx {
						displayURL = url[len(pfx):]
						break
					}
				}
				name := appName
				if name == "" {
					name = app
				}
				if len(displayURL) > 60 {
					displayURL = displayURL[:57] + "..."
				}
				return name + " → " + displayURL
			}
			if appName != "" {
				if title != "" && title != appName {
					return appName + " — " + title
				}
				return appName
			}
			if title != "" {
				return title
			}
			return app
		case event.EventTypeUIClick:
			controlName := e.Labels["control_name"]
			windowTitle := e.Labels["window_title"]
			appName := e.Labels["app_name"]
			app := e.Labels["app"]
			controlType := e.Labels["control_type"]

			display := appName
			if display == "" {
				display = windowTitle
			}
			if display == "" {
				display = app
			}

			if controlName != "" && controlName != "Chrome Legacy Window" {
				if controlType != "" {
					return controlName + " [" + controlType + "] — " + display
				}
				return controlName + " — " + display
			}
			if windowTitle != "" {
				return windowTitle + " — " + display
			}
			return display
		case event.EventTypeAppLaunch:
			appName := e.Labels["app_name"]
			app := e.Labels["app"]
			if appName != "" {
				return appName
			}
			return app
		case event.EventTypeTextInput:
			text, _ := e.Payload["text"].(string)
			controlName := e.Labels["control_name"]
			if len([]rune(text)) > 50 {
				text = string([]rune(text)[:47]) + "..."
			}
			if controlName != "" {
				return "[" + controlName + "] " + text
			}
			return text
		case event.EventTypeClipboardCopy:
			ct := e.Labels["content_type"]
			appName := e.Labels["app_name"]
			app := e.Labels["app"]
			src := appName
			if src == "" {
				src = app
			}
			prefix := src
			if prefix != "" {
				prefix += ": "
			}
			switch ct {
			case "image":
				dims := e.Labels["dimensions"]
				sizeKB, _ := e.Payload["size_kb"].(float64)
				if dims != "" {
					return fmt.Sprintf("%s复制图片 %s (~%dKB)", prefix, dims, int(sizeKB))
				}
				return prefix + "复制图片"
			case "files":
				count := e.Labels["file_count"]
				files, _ := e.Payload["files"].([]interface{})
				if len(files) == 1 {
					name, _ := files[0].(string)
					// Show only filename, not full path
					for i := len(name) - 1; i >= 0; i-- {
						if name[i] == '\\' || name[i] == '/' {
							name = name[i+1:]
							break
						}
					}
					return prefix + "复制文件: " + name
				}
				return fmt.Sprintf("%s复制 %s 个文件", prefix, count)
			default:
				text, _ := e.Payload["text"].(string)
				// Replace newlines for single-line display
				for _, nl := range []string{"\r\n", "\n", "\r"} {
					text = strings.ReplaceAll(text, nl, " ")
				}
				if len([]rune(text)) > 60 {
					text = string([]rune(text)[:57]) + "..."
				}
				if prefix != "" {
					return prefix + text
				}
				return text
			}
		default:
			appName := e.Labels["app_name"]
			if appName != "" {
				return appName
			}
			app := e.Labels["app"]
			return app
		}
	default:
		// Generic: show first label value
		for _, v := range e.Labels {
			return v
		}
		return string(e.Type)
	}
}

func parseSinceDuration(s string) int64 {
	if s == "" {
		return time.Now().Add(-24 * time.Hour).UnixMilli()
	}
	// Try duration format: 2h, 30m, 7d
	if len(s) > 0 {
		unit := s[len(s)-1]
		val, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err == nil {
			switch unit {
			case 'h':
				return time.Now().Add(-time.Duration(val * float64(time.Hour))).UnixMilli()
			case 'm':
				return time.Now().Add(-time.Duration(val * float64(time.Minute))).UnixMilli()
			case 'd':
				return time.Now().Add(-time.Duration(val * float64(24 * time.Hour))).UnixMilli()
			}
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d).UnixMilli()
	}
	return time.Now().Add(-24 * time.Hour).UnixMilli()
}

func formatNum(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case int64:
		return strconv.FormatInt(n, 10)
	case int:
		return strconv.Itoa(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
