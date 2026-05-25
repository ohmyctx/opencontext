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
	"syscall"
	"time"

	"github.com/spf13/cobra"

	claudecollector "github.com/opencontext/opencontext/collectors/claude"
	"github.com/opencontext/opencontext/pkg/client"
	"github.com/opencontext/opencontext/pkg/event"
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
  1 (L1, default) — command name only, e.g. "go" instead of "go build ./..."
  2 (L2)          — full command string including arguments`,
		Example: `  oc collector shell install
  oc collector shell install --sensitivity 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installShellHooks(sensitivity)
		},
	}

	cmd.Flags().IntVar(&sensitivity, "sensitivity", 1, "sensitivity level: 1=command name only, 2=full command with args")
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

_oc_precmd() {
  local _oc_exit=$?
  local _oc_end
  _oc_end=$(date +%%s%%3N)
  local _oc_dur=$(( _oc_end - ${_oc_cmd_start:-$_oc_end} ))

  [[ -z "$_oc_cmd_input" ]] && return 0

  (%s collector shell push \
    --command "$_oc_cmd_input" \
    --exit-code "$_oc_exit" \
    --duration-ms "$_oc_dur" \
    --cwd "$PWD" \
    --sensitivity %d &>/dev/null &)

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

_oc_precmd() {
  local _oc_exit=$?
  local _oc_end
  _oc_end=$(date +%%s%%3N 2>/dev/null || echo 0)
  local _oc_dur=$(( _oc_end - ${_oc_cmd_start:-0} ))

  [[ -z "$_oc_cmd_input" ]] && return 0
  [[ "$_oc_cmd_input" == "_oc_precmd" ]] && return 0

  (%s collector shell push \
    --command "$_oc_cmd_input" \
    --exit-code "$_oc_exit" \
    --duration-ms "$_oc_dur" \
    --cwd "$PWD" \
    --sensitivity %d &>/dev/null &)

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

	sensLabel := "L1 (command name only)"
	if sensitivity == 2 {
		sensLabel = "L2 (full command with args)"
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
	case event.SourceClaude:
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
