// oc is the OpenContext CLI. It communicates with the OpenContext daemon over HTTP and also
// exposes collector subcommands used by shell hooks.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ohmyctx/opencontext/internal/daemon"
	"github.com/ohmyctx/opencontext/internal/installers"
	"github.com/ohmyctx/opencontext/internal/registry"
	"github.com/ohmyctx/opencontext/internal/service"
	"github.com/ohmyctx/opencontext/internal/subscription"
	"github.com/ohmyctx/opencontext/pkg/client"
	"github.com/ohmyctx/opencontext/pkg/event"
)

var (
	daemonURL    string
	jsonOut      bool
	outputFormat string
	version      = "0.1.0"
)

func main() {
	root := buildRoot()
	if err := root.Execute(); err != nil {
		if jsonOut || outputFormat == "json" {
			_ = printErrorJSON(err)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:     "oc",
		Version: version,
		Short:   "OpenContext CLI — inspect events, subscriptions, memory, and collectors",
		Long: `oc is the command-line interface for OpenContext.

Agent workflow:
  1. Inspect commands with: oc schema --format json
  2. Start or verify the daemon with: oc daemon install && oc status
  3. Discover collectors with: oc collector list
  4. Install selected collectors with: oc collector <name> install
  5. Query events, inspect subscriptions, or compile memory

Environment variables:
  OC_DAEMON_URL    OpenContext daemon base URL (default: http://localhost:6060)`,
		Example: `  oc schema --format json
  oc status
  oc collector list
  oc collector browser-chrome install --dry-run
  oc event list --since 5m
  oc subscription list --format json
  oc memory compile --dry-run --format json`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return configureOutputMode()
		},
	}

	root.PersistentFlags().StringVar(&daemonURL, "daemon", envOrDefault("OC_DAEMON_URL", "http://localhost:6060"), "OpenContext daemon base URL")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "output as JSON")
	root.PersistentFlags().StringVar(&outputFormat, "format", "", "output format: json|table (default: table on TTY, json otherwise)")

	root.AddCommand(
		buildDaemonCmd(),
		buildStatusCmd(),
		buildEventCmd(),
		buildSubscriptionCmd(),
		buildMemoryCmd(),
		buildCollectorCmd(),
		buildSchemaCmd(root),
	)

	return root
}

func buildCollectorListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List known collector integrations",
		Long: `List collector manifests including name, kind, version, emitted sources,
and the command or guide used to install each collector.`,
		Example: `  oc collector list
  oc collector list --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			collectors := registry.AllCollectors()
			if jsonOut {
				return printJSON(withResolvedCollectorVersions(collectors))
			}
			fmt.Printf("%-12s %-18s %-16s %-20s %s\n", "NAME", "KIND", "VERSION", "SOURCES", "INSTALL")
			for _, c := range collectors {
				fmt.Printf("%-12s %-18s %-16s %-20s %s\n",
					c.Name,
					c.Kind,
					resolveCollectorVersion(c.Version),
					strings.Join(c.Sources, ","),
					strings.Join(c.Install, " && "),
				)
			}
			return nil
		},
	}
}

func buildCollectorInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show collector integration details",
		Long: `Show a single collector manifest with install command, supported platforms,
emitted sources, docs, and schema references.`,
		Example: `  oc collector info shell
  oc collector info browser-chrome --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := registry.LookupCollector(args[0])
			if !ok {
				return fmt.Errorf("unknown collector %q", args[0])
			}
			c.Version = resolveCollectorVersion(c.Version)
			if jsonOut {
				return printJSON(c)
			}
			fmt.Printf("name:        %s\n", c.Name)
			fmt.Printf("display:     %s\n", c.DisplayName)
			fmt.Printf("version:     %s\n", c.Version)
			fmt.Printf("kind:        %s\n", c.Kind)
			fmt.Printf("platforms:   %s\n", strings.Join(c.Platforms, ", "))
			fmt.Printf("sources:     %s\n", strings.Join(c.Sources, ", "))
			fmt.Printf("description: %s\n", c.Description)
			if len(c.Install) > 0 {
				fmt.Println("install:")
				for _, install := range c.Install {
					fmt.Printf("  %s\n", install)
				}
			}
			if c.Docs != "" {
				fmt.Printf("docs:        %s\n", c.Docs)
			}
			if len(c.Schemas) > 0 {
				fmt.Println("schemas:")
				for _, s := range c.Schemas {
					fmt.Printf("  %s.%s\n", s.Source, s.Type)
				}
			}
			return nil
		},
	}
}

func buildCollectorSchemasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schemas",
		Short: "List registered event schemas",
		Long: `List registered event schemas. Schemas are advisory metadata for
agents and memory rendering; events without schemas can still be ingested.`,
		Example: `  oc collector schemas
  oc collector schemas --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			schemas := event.AllSchemas()
			sortSchemas(schemas)
			if jsonOut {
				return printJSON(schemas)
			}
			fmt.Printf("%-24s %s\n", "EVENT", "DESCRIPTION")
			for _, s := range schemas {
				fmt.Printf("%-24s %s\n", fmt.Sprintf("%s.%s", s.Source, s.Type), s.Description)
			}
			return nil
		},
	}
}

// ── oc daemon ────────────────────────────────────────────────────────────────

func buildDaemonCmd() *cobra.Command {
	var cfgFile string
	var logLevel string

	cmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"start", "serve"},
		Short:   "Run the OpenContext local daemon",
		Long: `Run or manage the local OpenContext daemon.

For interactive debugging, run it in the foreground. For normal agent
installation, prefer oc daemon install so the daemon survives shell exits.`,
		Example: `  oc daemon install
  oc daemon status
  oc daemon
  oc daemon --log-level debug
  oc daemon restart`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonForeground(cfgFile, logLevel)
		},
	}

	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (default: ~/.opencontext/config.yaml)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
	cmd.AddCommand(buildDaemonRunCmd())
	cmd.AddCommand(buildDaemonInstallCmd())
	cmd.AddCommand(buildDaemonUninstallCmd())
	cmd.AddCommand(buildDaemonServiceCmd("start", "Start the installed daemon service", func(m service.Manager) error { return m.Start() }))
	cmd.AddCommand(buildDaemonServiceCmd("stop", "Stop the installed daemon service", func(m service.Manager) error { return m.Stop() }))
	cmd.AddCommand(buildDaemonServiceCmd("restart", "Restart the installed daemon service", func(m service.Manager) error { return m.Restart() }))
	cmd.AddCommand(buildDaemonStatusCmd())
	cmd.AddCommand(buildDaemonLogsCmd())
	return cmd
}

func runDaemonForeground(cfgFile, logLevel string) error {
	return daemon.Run(daemon.Options{
		ConfigFile: cfgFile,
		LogLevel:   logLevel,
		Version:    version,
	})
}

func buildDaemonRunCmd() *cobra.Command {
	var cfgFile string
	var logLevel string
	cmd := &cobra.Command{
		Use:    "run",
		Short:  "Run the daemon in the foreground",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonForeground(cfgFile, logLevel)
		},
	}
	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (default: ~/.opencontext/config.yaml)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
	return cmd
}

func buildDaemonInstallCmd() *cobra.Command {
	var cfg service.Config
	var force bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start OpenContext as a background service",
		Long: `Install and start OpenContext as a background service using the
best local service manager available for this machine.

This command is idempotent when the service is not installed. Use --force to
replace an existing installation.`,
		Example: `  oc daemon install
  oc daemon install --force
  oc daemon install --config ~/.opencontext/config.yaml
  oc daemon install --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.Resolve(&cfg); err != nil {
				return err
			}
			mgr, err := service.NewManager()
			if err != nil {
				return err
			}
			result := sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Resource:  "daemon",
				DryRun:    dryRun,
				Paths:     daemonInstallPaths(cfg),
				NextSteps: []string{"Run: oc daemon status --format json", "Run: oc status --format json"},
			}
			if dryRun {
				result.Status = "planned"
				result.Platform = mgr.Platform()
				if jsonOut {
					return printJSON(result)
				}
				printSideEffectPlan(result)
				return nil
			}
			if st, _ := mgr.Status(); st != nil && st.Installed && !force {
				return fmt.Errorf("daemon service already installed; use --force to reinstall")
			}
			result.Platform = mgr.Platform()
			install := func() error {
				if force {
					_ = mgr.Uninstall()
				}
				if err := mgr.Install(cfg); err != nil {
					return err
				}
				if err := service.SaveMeta(&service.Meta{
					LogFile:     cfg.LogFile,
					LogMaxSize:  cfg.LogMaxSize,
					WorkDir:     cfg.WorkDir,
					ConfigFile:  cfg.ConfigFile,
					BinaryPath:  cfg.BinaryPath,
					Platform:    mgr.Platform(),
					InstalledAt: service.NowISO(),
				}); err != nil {
					return fmt.Errorf("save daemon metadata: %w", err)
				}
				fmt.Println("OpenContext daemon installed and started.")
				fmt.Printf("  platform: %s\n", mgr.Platform())
				fmt.Printf("  binary:   %s\n", cfg.BinaryPath)
				fmt.Printf("  workdir:  %s\n", cfg.WorkDir)
				fmt.Printf("  log:      %s\n", cfg.LogFile)
				if strings.Contains(mgr.Platform(), "user") {
					if enabled, user := service.CheckLinger(); !enabled {
						fmt.Printf("\nWarning: user service may stop after logout. To keep it alive, run: sudo loginctl enable-linger %s\n", user)
					}
				}
				return nil
			}
			if jsonOut {
				output, err := captureStdout(install)
				if err != nil {
					return err
				}
				result.Output = strings.TrimSpace(output)
				return printJSON(result)
			}
			return install()
		},
	}
	cmd.Flags().StringVar(&cfg.ConfigFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	cmd.Flags().StringVar(&cfg.WorkDir, "work-dir", "", "working directory (default: current directory)")
	cmd.Flags().StringVar(&cfg.LogFile, "log-file", "", "log file path (default: ~/.opencontext/logs/oc.log)")
	cmd.Flags().Int64Var(&cfg.LogMaxSize, "log-max-size", 10, "max log size in MB")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing service installation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview service files and metadata without installing")
	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		cfg.LogMaxSize *= 1024 * 1024
	}
	return markSideEffect(cmd, false)
}

func buildDaemonUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed daemon service",
		Example: `  oc daemon uninstall
  oc daemon uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.NewManager()
			if err != nil {
				return err
			}
			result := sideEffectResult{
				Status:   "uninstalled",
				Action:   "uninstall",
				Resource: "daemon",
				DryRun:   dryRun,
				Platform: mgr.Platform(),
				Paths:    daemonUninstallPaths(),
			}
			if dryRun {
				result.Status = "planned"
				if jsonOut {
					return printJSON(result)
				}
				printSideEffectPlan(result)
				return nil
			}
			if err := mgr.Uninstall(); err != nil {
				return err
			}
			service.RemoveMeta()
			if jsonOut {
				return printJSON(result)
			}
			fmt.Println("OpenContext daemon uninstalled.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview service removal without uninstalling")
	return markSideEffect(cmd, true)
}

func buildDaemonServiceCmd(use, short string, action func(service.Manager) error) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Example: fmt.Sprintf("  oc daemon %s\n  oc daemon %s --dry-run --format json", use, use),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.NewManager()
			if err != nil {
				return err
			}
			result := sideEffectResult{
				Status:   map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[use],
				Action:   use,
				Resource: "daemon",
				DryRun:   dryRun,
				Platform: mgr.Platform(),
				Paths:    daemonUninstallPaths(),
			}
			if dryRun {
				result.Status = "planned"
				if jsonOut {
					return printJSON(result)
				}
				printSideEffectPlan(result)
				return nil
			}
			if err := requireServiceInstalled(mgr); err != nil {
				return err
			}
			if err := action(mgr); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(result)
			}
			past := result.Status
			fmt.Printf("OpenContext daemon %s.\n", past)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview service action without changing daemon state")
	return markSideEffect(cmd, use == "stop" || use == "restart")
}

func buildDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show background daemon service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.NewManager()
			if err != nil {
				return err
			}
			st, err := mgr.Status()
			if err != nil {
				return err
			}
			state := "stopped"
			if st.Running {
				state = "running"
			}
			if !st.Installed {
				state = "not installed"
			}
			result := map[string]any{
				"status":    state,
				"installed": st.Installed,
				"running":   st.Running,
				"platform":  st.Platform,
			}
			if st.PID > 0 {
				result["pid"] = st.PID
			}
			if meta, err := service.LoadMeta(); err == nil {
				result["log"] = meta.LogFile
				result["workdir"] = meta.WorkDir
			}
			if jsonOut {
				return printJSON(result)
			}
			fmt.Println("OpenContext daemon service")
			fmt.Printf("  status:   %s\n", state)
			fmt.Printf("  platform: %s\n", st.Platform)
			if st.PID > 0 {
				fmt.Printf("  pid:      %d\n", st.PID)
			}
			if meta, err := service.LoadMeta(); err == nil {
				fmt.Printf("  log:      %s\n", meta.LogFile)
				fmt.Printf("  workdir:  %s\n", meta.WorkDir)
			}
			return nil
		},
	}
}

func buildDaemonLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	var logFile string
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show daemon log output",
		RunE: func(cmd *cobra.Command, args []string) error {
			if logFile == "" {
				if meta, err := service.LoadMeta(); err == nil && meta.LogFile != "" {
					logFile = meta.LogFile
				} else {
					logFile = service.DefaultLogFile()
				}
			}
			if err := printLastLines(logFile, lines); err != nil {
				return err
			}
			if follow {
				return followFile(logFile)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines to show")
	cmd.Flags().StringVar(&logFile, "log-file", "", "custom log file path")
	return cmd
}

func requireServiceInstalled(mgr service.Manager) error {
	st, err := mgr.Status()
	if err != nil {
		return err
	}
	if st == nil || !st.Installed {
		return fmt.Errorf("daemon service is not installed; run: oc daemon install")
	}
	return nil
}

// ── oc status ─────────────────────────────────────────────────────────────────

func buildStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show OpenContext daemon health and statistics",
		Long: `Check whether the OpenContext daemon HTTP API is reachable and return
health statistics such as version, uptime, and stored event count.`,
		Example: `  oc status
  oc status --format json
  oc status --daemon http://127.0.0.1:6060`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			health, err := c.Health(ctx)
			if err != nil {
				return fmt.Errorf("OpenContext daemon unreachable at %s: %w\n\nStart it with: oc daemon", daemonURL, err)
			}

			if jsonOut {
				return printJSON(health)
			}

			fmt.Printf("daemon status:   %s\n", health["status"])
			fmt.Printf("version:         %s\n", health["version"])
			fmt.Printf("uptime:          %ss\n", formatNum(health["uptime_seconds"]))
			fmt.Printf("events stored:   %s\n", formatNum(health["events_stored"]))
			fmt.Printf("daemon URL:      %s\n", daemonURL)
			return nil
		},
	}
}

// ── oc subscription ───────────────────────────────────────────────────────────

type subscriptionView struct {
	Name            string            `json:"name"`
	Sources         []event.Source    `json:"sources,omitempty"`
	LabelSelectors  map[string]string `json:"label_selectors,omitempty"`
	MaxSensitivity  int               `json:"max_sensitivity"`
	Backend         string            `json:"backend"`
	MemoryPath      string            `json:"memory_path,omitempty"`
	ClaudeMD        string            `json:"claude_md,omitempty"`
	AgentsMD        string            `json:"agents_md,omitempty"`
	CursorRulesDir  string            `json:"cursor_rules_dir,omitempty"`
	InjectTargets   []string          `json:"inject_targets,omitempty"`
	RefreshInterval string            `json:"refresh_interval,omitempty"`
	Schedule        string            `json:"schedule,omitempty"`
	LLMProvider     string            `json:"llm_provider,omitempty"`
	LLMModel        string            `json:"llm_model,omitempty"`
}

func buildSubscriptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscription",
		Short: "Inspect memory subscriptions",
		Long: `Inspect memory subscriptions from the local OpenContext config.

A subscription chooses which event sources and labels become memory, which
backend renders that memory, and which files receive it.`,
		Example: `  oc subscription list --format json
  oc subscription info global --format json`,
	}
	cmd.AddCommand(buildSubscriptionListCmd())
	cmd.AddCommand(buildSubscriptionInfoCmd())
	return cmd
}

func buildSubscriptionListCmd() *cobra.Command {
	var configFile string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured memory subscriptions",
		Long:  `List configured memory subscriptions from ~/.opencontext/config.yaml or --config.`,
		Example: `  oc subscription list
  oc subscription list --config ~/.opencontext/config.yaml --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := subscription.Load(configFile)
			if err != nil {
				return err
			}
			views := subscriptionViews(cfg.Subscriptions)
			if jsonOut {
				return printJSON(map[string]any{
					"config_file":   cfg.ConfigFile,
					"subscriptions": views,
					"total":         len(views),
				})
			}
			if len(views) == 0 {
				fmt.Println("No subscriptions configured.")
				return nil
			}
			fmt.Printf("%-24s %-10s %-18s %s\n", "NAME", "BACKEND", "SOURCES", "MEMORY")
			for _, v := range views {
				fmt.Printf("%-24s %-10s %-18s %s\n", v.Name, v.Backend, formatSources(v.Sources), v.MemoryPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	return cmd
}

func buildSubscriptionInfoCmd() *cobra.Command {
	var configFile string
	cmd := &cobra.Command{
		Use:   "info <name>",
		Short: "Show one memory subscription",
		Long:  `Show one configured memory subscription, including filters, memory path, targets, schedule, and backend.`,
		Example: `  oc subscription info global
  oc subscription info opencontext-project --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := subscription.Load(configFile)
			if err != nil {
				return err
			}
			for _, sub := range cfg.Subscriptions {
				if sub.Name != args[0] {
					continue
				}
				view := subscriptionViewOf(sub)
				if jsonOut {
					return printJSON(map[string]any{
						"config_file":  cfg.ConfigFile,
						"subscription": view,
					})
				}
				fmt.Printf("name:             %s\n", view.Name)
				fmt.Printf("backend:          %s\n", view.Backend)
				fmt.Printf("sources:          %s\n", formatSources(view.Sources))
				fmt.Printf("max_sensitivity:  %d\n", view.MaxSensitivity)
				fmt.Printf("memory_path:      %s\n", view.MemoryPath)
				if len(view.LabelSelectors) > 0 {
					fmt.Printf("label_selectors:  %v\n", view.LabelSelectors)
				}
				if len(view.InjectTargets) > 0 {
					fmt.Println("inject_targets:")
					for _, target := range view.InjectTargets {
						fmt.Printf("  %s\n", target)
					}
				}
				return nil
			}
			return fmt.Errorf("unknown subscription %q", args[0])
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	return cmd
}

func subscriptionViews(subs []subscription.Subscription) []subscriptionView {
	views := make([]subscriptionView, 0, len(subs))
	for _, sub := range subs {
		views = append(views, subscriptionViewOf(sub))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

func subscriptionViewOf(sub subscription.Subscription) subscriptionView {
	targets := []string{}
	if sub.Memory.ClaudeMD != "" {
		targets = append(targets, sub.Memory.ClaudeMD)
	}
	if sub.Memory.AgentsMD != "" {
		targets = append(targets, sub.Memory.AgentsMD)
	}
	if sub.Memory.CursorRulesDir != "" {
		targets = append(targets, filepath.Join(sub.Memory.CursorRulesDir, "opencontext-memory.mdc"))
	}
	for _, t := range sub.Memory.InjectTargets {
		targets = append(targets, t.Path)
	}
	view := subscriptionView{
		Name:            sub.Name,
		Sources:         sub.Filter.Sources,
		LabelSelectors:  sub.Filter.LabelSelectors,
		MaxSensitivity:  int(sub.MaxSensitivity()),
		Backend:         string(sub.Memory.Backend),
		MemoryPath:      sub.Memory.Path,
		ClaudeMD:        sub.Memory.ClaudeMD,
		AgentsMD:        sub.Memory.AgentsMD,
		CursorRulesDir:  sub.Memory.CursorRulesDir,
		InjectTargets:   targets,
		RefreshInterval: sub.EffectiveRefreshInterval().String(),
		Schedule:        sub.Schedule,
	}
	if view.Backend == "" {
		view.Backend = string(subscription.BackendFile)
	}
	if sub.LLM != nil {
		view.LLMProvider = sub.LLM.Provider
		view.LLMModel = sub.LLM.Model
	}
	return view
}

func formatSources(sources []event.Source) string {
	if len(sources) == 0 {
		return "all"
	}
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ",")
}

// ── oc event ──────────────────────────────────────────────────────────────────

func buildEventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Query and manage raw activity events",
		Long: `Query and manage raw activity events from the local daemon.

Events are the append-only activity facts collected from shells, browsers,
agent hooks, OS collectors, Git hooks, and other sources.`,
		Example: `  oc event list --since 5m
  oc event list --source shell --format json
  oc event clear --source git --dry-run --format json`,
	}
	cmd.AddCommand(buildEventListCmd())
	cmd.AddCommand(buildEventClearCmd())
	return cmd
}

func buildEventListCmd() *cobra.Command {
	var (
		source  string
		project string
		since   string
		limit   int
		query   string
		maxSens int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent activity events",
		Long: `Query recent activity events from the local daemon.

When stdout is not a TTY, output defaults to JSON for agent parsing. Use
--format table when a human-readable table is explicitly needed.`,
		Example: `  oc event list
  oc event list --since 5m
  oc event list --source shell --project opencontext --since 2h
  oc event list --query "go build" --format json
  oc event list --since 5m --format table`,
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
			if maxSens > 0 {
				q.MaxSensitivity = event.SensitivityLevel(maxSens)
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
	cmd.Flags().IntVar(&maxSens, "max-sensitivity", 0, "maximum sensitivity to return (1=L1, 2=L2, 3=L3; default: all stored events)")
	return cmd
}

func buildEventClearCmd() *cobra.Command {
	var source string
	var clearDryRun bool
	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete stored events",
		Long: `Delete stored events from the local daemon. Use --source to delete only
events from one source. This is destructive.`,
		Example: `  oc event clear           # delete all events
  oc event clear --source shell  # delete shell events only
  oc event clear --source browser --format json
  oc event clear --source git --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clearDryRun {
				result := map[string]any{
					"status":     "planned",
					"action":     "clear",
					"resource":   "events",
					"dry_run":    true,
					"daemon_url": daemonURL,
				}
				if source != "" {
					result["source"] = source
				} else {
					result["scope"] = "all"
				}
				if jsonOut {
					return printJSON(result)
				}
				if source != "" {
					fmt.Printf("Events clear dry run for source: %s\n", source)
				} else {
					fmt.Println("Events clear dry run for all sources.")
				}
				return nil
			}
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if source != "" {
				if err := c.DeleteEventsBySource(ctx, source); err != nil {
					return fmt.Errorf("delete %s events: %w", source, err)
				}
				if jsonOut {
					return printJSON(map[string]any{"status": "deleted", "source": source})
				}
				fmt.Printf("Deleted all events with source: %s\n", source)
				return nil
			}

			if err := c.DeleteAllEvents(ctx); err != nil {
				return fmt.Errorf("delete events: %w", err)
			}
			if jsonOut {
				return printJSON(map[string]any{"status": "deleted"})
			}
			fmt.Println("Deleted all events.")
			return nil
		},
	}
	clearCmd.Flags().StringVar(&source, "source", "", "delete events from a specific source (shell|git|os|browser|ide|im)")
	clearCmd.Flags().BoolVar(&clearDryRun, "dry-run", false, "preview deletion without deleting events")
	markSideEffect(clearCmd, true)
	return clearCmd
}

// ── oc memory ─────────────────────────────────────────────────────────────────

func buildMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Compile memory and manage memory output targets",
		Long: `Manage agent-readable memory generated from OpenContext events.

Memory is produced from subscriptions. A subscription chooses which events are
included and where the canonical memory file is written. Targets are additional
agent files that receive injected memory sections.`,
		Example: `  oc memory compile --subscription global
  oc memory target add hermes --dry-run --format json`,
	}
	cmd.AddCommand(buildMemoryCompileCmd())
	cmd.AddCommand(buildMemoryTargetCmd())
	return cmd
}

func buildMemoryCompileCmd() *cobra.Command {
	var subName string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Trigger memory compilation for a subscription",
		Long: `Trigger memory compilation for one subscription or all subscriptions.

Compilation is asynchronous. After triggering, inspect the configured
memory.md path or wait for the raw_dump refresh interval.`,
		Example: `  oc memory compile
  oc memory compile --subscription opencontext-project
  oc memory compile --subscription claudecode-context --format json
  oc memory compile --subscription opencontext-project --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			subscription := subName
			if subscription == "" {
				subscription = "all"
			}
			result := sideEffectResult{
				Status:    "triggered",
				Action:    "compile",
				Resource:  "memory",
				DryRun:    dryRun,
				DaemonURL: daemonURL,
				NextSteps: []string{"Check the configured memory.md path after a short delay."},
			}
			if dryRun {
				result.Status = "planned"
				if jsonOut {
					return printJSON(map[string]any{
						"status":       result.Status,
						"action":       result.Action,
						"resource":     result.Resource,
						"dry_run":      true,
						"daemon_url":   daemonURL,
						"subscription": subscription,
						"async":        true,
						"next_steps":   result.NextSteps,
					})
				}
				fmt.Printf("Memory compile dry run for subscription: %s\n", subscription)
				fmt.Printf("  daemon: %s\n", daemonURL)
				return nil
			}
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := c.TriggerCompile(ctx, subName); err != nil {
				return fmt.Errorf("trigger compile: %w", err)
			}

			if jsonOut {
				return printJSON(map[string]any{
					"status":       "triggered",
					"action":       "compile",
					"resource":     "memory",
					"subscription": subscription,
					"async":        true,
					"suggestion":   "Check the configured memory.md path after a short delay.",
				})
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview compile trigger without contacting the daemon")
	return markSideEffect(cmd, false)
}

func buildMemoryTargetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "target",
		Short: "Manage additional memory injection targets",
		Long: `Manage additional memory targets that receive the compiled memory section.

Targets are stored in the selected subscription config as memory.inject_targets.
They do not compile memory by themselves; run oc memory compile or wait for the
subscription refresh interval after adding a target.`,
		Example: `  oc memory target add hermes
  oc memory target add openclaw --dry-run --format json`,
	}
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a memory injection target",
		Long:  `Add an agent memory file as an OpenContext memory injection target.`,
		Example: `  oc memory target add hermes
  oc memory target add openclaw --dry-run --format json`,
	}
	add.AddCommand(buildInjectHermesCmd())
	add.AddCommand(buildInjectOpenClawCmd())
	cmd.AddCommand(add)
	return cmd
}

// ── oc collector ─────────────────────────────────────────────────────────────

func buildCollectorCmd() *cobra.Command {
	collector := &cobra.Command{
		Use:   "collector",
		Short: "Collector management subcommands",
		Long: `Discover, inspect, install, or operate collector integrations.

Agent workflow:
  1. oc collector list --format json
  2. oc collector info <name> --format json
  3. oc schema collector <name> install --format json
  4. oc collector <name> install

Most install commands make local configuration changes, so agents should inspect
the command schema first and use --dry-run when available.`,
		Example: `  oc collector list --format json
  oc collector info browser-chrome --format json
  oc schema collector shell install --format json
  oc collector shell install
  oc collector browser-chrome install --dry-run`,
	}
	collector.AddCommand(buildCollectorListCmd())
	collector.AddCommand(buildCollectorInfoCmd())
	collector.AddCommand(buildCollectorSchemasCmd())
	collector.AddCommand(buildShellCollectorCmd())
	collector.AddCommand(buildGitCollectorCmd())
	collector.AddCommand(buildClaudeCollectorCmd())
	collector.AddCommand(buildCodexCollectorCmd())
	collector.AddCommand(buildCursorCollectorCmd())
	collector.AddCommand(buildOpenCodeCollectorCmd())
	collector.AddCommand(buildOpenClawCollectorCmd())
	collector.AddCommand(buildHermesCollectorCmd())
	collector.AddCommand(buildBrowserChromeCollectorCmd())
	collector.AddCommand(buildBrowserFirefoxCollectorCmd())
	collector.AddCommand(buildBrowserEdgeCollectorCmd())
	return collector
}

func buildShellCollectorCmd() *cobra.Command {
	shell := &cobra.Command{
		Use:   "shell",
		Short: "Shell collector commands",
		Long: `Install shell hooks or push shell command events. The push subcommand
is intended for generated hook scripts; users usually only run install.`,
		Example: `  oc collector shell install
  oc collector shell install --sensitivity 2
  oc collector shell push --command "go test ./..." --cwd "$PWD" --sensitivity 2`,
	}
	shell.AddCommand(buildShellPushCmd())
	shell.AddCommand(buildShellInstallCmd())
	shell.AddCommand(buildShellUninstallCmd())
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
		Short: "Push a shell command event to the OpenContext daemon",
		Long: `Push is called by shell hook scripts (zsh preexec/precmd) to record
a command execution event. It runs non-blocking and silently ignores
the OpenContext daemon being unavailable.`,
		Example: `  oc collector shell push --command "go test ./..." --exit-code 0 --duration-ms 1200 --cwd "$PWD"
  oc collector shell push --command "git status" --sensitivity 1`,
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
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install shell hooks for zsh and bash",
		Long: `Install shell hooks that record commands to the OpenContext daemon.

Sensitivity levels:
  1 (L1) — command name only, e.g. "go" instead of "go build ./..."
  2 (L2, default) — full command string including arguments`,
		Example: `  oc collector shell install
  oc collector shell install --sensitivity 2
  oc collector shell install --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "shell",
				DryRun:    dryRun,
				Paths:     shellCollectorPaths(),
				NextSteps: []string{"Restart your shell or source your shell config."},
			}, dryRun, func() error {
				return installers.InstallShell(sensitivity)
			})
		},
	}

	cmd.Flags().IntVar(&sensitivity, "sensitivity", 2, "sensitivity level: 1=command name only, 2=full command with args")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview files and shell profiles without writing changes")
	return cmd
}

func buildShellUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove shell hooks from zsh, bash, and PowerShell",
		Long: `Uninstall removes OpenContext shell hooks from shell configuration files
(.zshrc, .bashrc, PowerShell profiles) and deletes the hooks directory.`,
		Example: `  oc collector shell uninstall
  oc collector shell uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "shell",
				DryRun:    dryRun,
				Paths:     shellCollectorPaths(),
			}, dryRun, installers.UninstallShell)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removed files and profile entries without writing changes")
	return cmd
}

// ── git collector ─────────────────────────────────────────────────────────────

func buildGitCollectorCmd() *cobra.Command {
	git := &cobra.Command{
		Use:   "git",
		Short: "Git hook collector commands",
		Long: `Install repository-local Git hooks or push Git events. The push
subcommand is intended for generated hook scripts; users usually only run
install or uninstall.`,
		Example: `  oc collector git install --repo .
  oc collector git uninstall --repo .
  oc collector git push --hook post-commit --repo "$PWD"`,
	}
	git.AddCommand(buildGitPushCmd())
	git.AddCommand(buildGitInstallCmd())
	git.AddCommand(buildGitUninstallCmd())
	return git
}

func buildGitInstallCmd() *cobra.Command {
	var repo string
	var sensitivity int
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext Git hooks into a repository",
		Long: `Installs repository-local hooks for meaningful Git events:
post-commit, post-checkout, post-merge, and pre-push.

Existing hooks are preserved by moving them to an OpenContext backup path and
calling them from the generated wrapper. Hooks run non-blocking and should never
slow down Git if the OpenContext daemon is unavailable.`,
		Example: `  oc collector git install --repo .
  oc collector git install --repo /path/to/repo --sensitivity 2
  oc collector git install --repo . --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" {
				repo = "."
			}
			paths, err := gitCollectorPaths(repo)
			if err != nil {
				return err
			}
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "git",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     paths,
				NextSteps: []string{"Make a commit, branch switch, merge, or push, then run: oc event list --source git --since 10m --format json"},
			}, dryRun, func() error {
				return installers.InstallGit(repo, daemonAddr, sensitivity)
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", ".", "repository path")
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().IntVar(&sensitivity, "sensitivity", 2, "sensitivity level: 1=metadata only, 2=commit messages and stats")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook files without writing changes")
	return cmd
}

func buildGitUninstallCmd() *cobra.Command {
	var repo string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext Git hooks from a repository",
		Long:  `Removes OpenContext-generated Git hook wrappers and restores backed-up hooks when present.`,
		Example: `  oc collector git uninstall --repo .
  oc collector git uninstall --repo /path/to/repo
  oc collector git uninstall --repo . --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" {
				repo = "."
			}
			paths, err := gitCollectorPaths(repo)
			if err != nil {
				return err
			}
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "git",
				DryRun:    dryRun,
				Paths:     paths,
			}, dryRun, func() error {
				return installers.UninstallGit(repo)
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", ".", "repository path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook removal without writing changes")
	return cmd
}

func buildGitPushCmd() *cobra.Command {
	var (
		hook        string
		repo        string
		oldRef      string
		newRef      string
		flag        string
		remote      string
		remoteURL   string
		sensitivity int
	)

	cmd := &cobra.Command{
		Use:    "push",
		Hidden: true,
		Short:  "Push a Git hook event to the OpenContext daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" {
				repo = "."
			}
			e, err := buildGitHookEvent(hook, repo, oldRef, newRef, flag, remote, remoteURL, sensitivity, cmd.InOrStdin())
			if err != nil || e == nil {
				return nil
			}
			c := client.New(daemonURL)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = c.Push(ctx, e)
			return nil
		},
	}
	cmd.Flags().StringVar(&hook, "hook", "", "git hook name")
	cmd.Flags().StringVar(&repo, "repo", ".", "repository path")
	cmd.Flags().StringVar(&oldRef, "old", "", "old ref/hash from git hook")
	cmd.Flags().StringVar(&newRef, "new", "", "new ref/hash from git hook")
	cmd.Flags().StringVar(&flag, "flag", "", "checkout flag from post-checkout")
	cmd.Flags().StringVar(&remote, "remote", "", "remote name from pre-push")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "remote URL from pre-push")
	cmd.Flags().IntVar(&sensitivity, "sensitivity", 2, "sensitivity level")
	return cmd
}

// ── claude collector ──────────────────────────────────────────────────────────

func buildClaudeCollectorCmd() *cobra.Command {
	claude := &cobra.Command{
		Use:   "claude",
		Short: "Claude Code hook collector commands",
		Long: `Install Claude Code HTTP hooks so user prompts and session starts are
posted to the OpenContext daemon.`,
		Example: `  oc collector claude install
  oc collector claude install --daemon http://127.0.0.1:6060`,
	}
	claude.AddCommand(buildClaudeInstallCmd())
	claude.AddCommand(buildClaudeUninstallCmd())
	return claude
}

func buildClaudeInstallCmd() *cobra.Command {
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext HTTP hooks into Claude Code",
		Long: `Adds UserPromptSubmit and SessionStart HTTP hooks to Claude Code.
Claude Code will POST each user message to the OpenContext daemon for recording.`,
		Example: `  oc collector claude install
  oc collector claude install --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "claude",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     homePaths(".claude/settings.json"),
				NextSteps: []string{"Start the daemon with 'oc daemon', then open a Claude Code session."},
			}, dryRun, func() error {
				return installers.InstallClaude(daemonAddr)
			})
		},
	}

	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview settings changes without writing files")
	return cmd
}

func buildClaudeUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext HTTP hooks from Claude Code settings",
		Long: `Removes UserPromptSubmit and SessionStart HTTP hook entries from
~/.claude/settings.json that point to the OpenContext daemon.`,
		Example: `  oc collector claude uninstall
  oc collector claude uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "claude",
				DryRun:    dryRun,
				Paths:     homePaths(".claude/settings.json"),
			}, dryRun, installers.UninstallClaude)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview settings changes without writing files")
	return cmd
}

// ── Codex CLI collector ───────────────────────────────────────────────────────

func buildCodexCollectorCmd() *cobra.Command {
	codex := &cobra.Command{
		Use:   "codex",
		Short: "OpenAI Codex CLI hook collector commands",
		Long: `Install Codex CLI hook scripts so user prompts and session starts are
posted to the OpenContext daemon.`,
		Example: `  oc collector codex install
  oc collector codex install --daemon http://127.0.0.1:6060`,
	}
	codex.AddCommand(buildCodexInstallCmd())
	codex.AddCommand(buildCodexUninstallCmd())
	return codex
}

func buildCodexInstallCmd() *cobra.Command {
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext hooks into Codex CLI (~/.codex/config.json)",
		Long: `Adds UserPromptSubmit and SessionStart HTTP hooks to Codex CLI.
Codex will POST each user message to the OpenContext daemon for recording.

Requires Codex CLI with hooks support (codex >= 0.1.x).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "codex",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     homePaths(".opencontext/collectors/hooks/codex.sh", ".codex/hooks.json"),
				NextSteps: []string{"Start the daemon with 'oc daemon', then open a Codex session."},
			}, dryRun, func() error {
				return installers.InstallCodex(daemonAddr)
			})
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook files and settings changes without writing files")
	return cmd
}

func buildCodexUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext hooks from Codex CLI",
		Long:  `Removes hook script files and hook entries from ~/.codex/hooks.json.`,
		Example: `  oc collector codex uninstall
  oc collector codex uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "codex",
				DryRun:    dryRun,
				Paths:     homePaths(".opencontext/collectors/hooks", ".codex/hooks.json"),
			}, dryRun, installers.UninstallCodex)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removed files and settings changes without writing files")
	return cmd
}

// ── Cursor IDE collector ──────────────────────────────────────────────────────

func buildCursorCollectorCmd() *cobra.Command {
	cursor := &cobra.Command{
		Use:   "cursor",
		Short: "Cursor IDE agent hook collector commands",
		Long: `Install Cursor hook scripts so agent prompt submissions and session
starts are posted to the OpenContext daemon.`,
		Example: `  oc collector cursor install
  oc collector cursor install --daemon http://127.0.0.1:6060`,
	}
	cursor.AddCommand(buildCursorInstallCmd())
	cursor.AddCommand(buildCursorUninstallCmd())
	return cursor
}

func buildCursorInstallCmd() *cobra.Command {
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext hooks into Cursor IDE (~/.cursor/hooks.json)",
		Long: `Adds beforeSubmitPrompt and sessionStart command hooks to Cursor IDE.
Cursor will execute the hook script on each user prompt submission.

Requires Cursor IDE with hooks support (Cursor >= 1.0).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "cursor",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     homePaths(".cursor/hooks/oc-capture.sh", ".cursor/hooks.json"),
				NextSteps: []string{"Reload Cursor. Agent prompts and session starts will be recorded."},
			}, dryRun, func() error {
				return installers.InstallCursor(daemonAddr)
			})
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook files and settings changes without writing files")
	return cmd
}

func buildCursorUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext hooks from Cursor IDE",
		Long:  `Removes hook script files and hook entries from ~/.cursor/hooks.json.`,
		Example: `  oc collector cursor uninstall
  oc collector cursor uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "cursor",
				DryRun:    dryRun,
				Paths:     homePaths(".cursor/hooks", ".cursor/hooks.json"),
			}, dryRun, installers.UninstallCursor)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removed files and settings changes without writing files")
	return cmd
}

// ── OpenCode collector ────────────────────────────────────────────────────────

func buildOpenCodeCollectorCmd() *cobra.Command {
	opencode := &cobra.Command{
		Use:   "opencode",
		Short: "OpenCode (sst/opencode) hook collector commands",
		Long: `Install OpenCode hook scripts so user messages and session starts are
posted to the OpenContext daemon.`,
		Example: `  oc collector opencode install
  oc collector opencode install --daemon http://127.0.0.1:6060`,
	}
	opencode.AddCommand(buildOpenCodeInstallCmd())
	opencode.AddCommand(buildOpenCodeUninstallCmd())
	return opencode
}

func buildOpenCodeInstallCmd() *cobra.Command {
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext hooks into OpenCode (~/.config/opencode/hooks.json)",
		Long: `Adds UserPromptSubmit and SessionStart command hooks to OpenCode.
OpenCode will execute the hook script on each user message submission.

Supports both the native opencode hook format and the Claude-compatible
format (via opencode-claude-hooks npm package).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "opencode",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     homePaths(".opencontext/collectors/hooks/opencode.sh", ".config/opencode/hooks.json"),
				NextSteps: []string{"Start or restart OpenCode. User messages will be recorded."},
			}, dryRun, func() error {
				return installers.InstallOpenCode(daemonAddr)
			})
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook files and settings changes without writing files")
	return cmd
}

func buildOpenCodeUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext hooks from OpenCode",
		Long:  `Removes hook script files and hook entries from ~/.config/opencode/hooks.json.`,
		Example: `  oc collector opencode uninstall
  oc collector opencode uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "opencode",
				DryRun:    dryRun,
				Paths:     homePaths(".opencontext/collectors/hooks", ".config/opencode/hooks.json"),
			}, dryRun, installers.UninstallOpenCode)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removed files and settings changes without writing files")
	return cmd
}

// ── OpenClaw collector ────────────────────────────────────────────────────────

func buildOpenClawCollectorCmd() *cobra.Command {
	openclaw := &cobra.Command{
		Use:   "openclaw",
		Short: "OpenClaw hook collector commands",
		Long: `Install the OpenContext internal hook into OpenClaw so user messages
and session starts are posted to the OpenContext daemon.`,
		Example: `  oc collector openclaw install
  oc collector openclaw install --daemon http://127.0.0.1:6060`,
	}
	openclaw.AddCommand(buildOpenClawInstallCmd())
	openclaw.AddCommand(buildOpenClawUninstallCmd())
	return openclaw
}

func buildOpenClawInstallCmd() *cobra.Command {
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext internal hook into OpenClaw",
		Long: `Creates an OpenClaw internal hook in ~/.opencontext/collectors/openclaw-hooks/opencontext/
containing HOOK.md and handler.js, then registers the scan directory via:
  openclaw config patch --stdin

The hook fires on message_received (user messages) and session_start events,
forwarding them to the OpenContext daemon for recording.

Requires OpenClaw >= 2026.3 with internal hooks support.
Restart OpenClaw after installation.`,
		Example: `  oc collector openclaw install
  oc collector openclaw install --daemon http://127.0.0.1:6060
  oc collector openclaw install --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "openclaw",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     homePaths(".opencontext/collectors/openclaw-hooks/opencontext", ".opencontext/collectors/openclaw-watcher/watch.py"),
				NextSteps: []string{"Restart OpenClaw.", "For TUI fallback, run: python3 ~/.opencontext/collectors/openclaw-watcher/watch.py &"},
			}, dryRun, func() error {
				return installers.InstallOpenClaw(daemonAddr)
			})
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook files and config changes without writing files")
	return cmd
}

func buildOpenClawUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext internal hook from OpenClaw",
		Long:  `Removes the ~/.opencontext/collectors/openclaw-hooks/opencontext/ directory.`,
		Example: `  oc collector openclaw uninstall
  oc collector openclaw uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "openclaw",
				DryRun:    dryRun,
				Paths:     homePaths(".opencontext/collectors/openclaw-hooks/opencontext"),
			}, dryRun, installers.UninstallOpenClaw)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removed files without writing changes")
	return cmd
}

// ── Hermes Agent collector ────────────────────────────────────────────────────

func buildHermesCollectorCmd() *cobra.Command {
	hermes := &cobra.Command{
		Use:   "hermes",
		Short: "Hermes Agent gateway hook collector commands",
		Long: `Install the OpenContext gateway hook into Hermes Agent so agent messages
and session starts are posted to the OpenContext daemon.`,
		Example: `  oc collector hermes install
  oc collector hermes install --daemon http://127.0.0.1:6060`,
	}
	hermes.AddCommand(buildHermesInstallCmd())
	hermes.AddCommand(buildHermesUninstallCmd())
	return hermes
}

func buildHermesInstallCmd() *cobra.Command {
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install OpenContext gateway hook into Hermes Agent (~/.hermes/hooks/opencontext/)",
		Long: `Creates ~/.hermes/hooks/opencontext/HOOK.yaml and handler.py.
The gateway hook fires on agent:start (user message) and session:start events,
forwarding them to the OpenContext daemon for recording.

Requires Hermes Agent with gateway hook support (hermes >= 0.4).
Restart the Hermes gateway after installation: hermes gateway`,
		Example: `  oc collector hermes install
  oc collector hermes install --daemon http://127.0.0.1:6060
  oc collector hermes install --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "installed",
				Action:    "install",
				Collector: "hermes",
				DryRun:    dryRun,
				DaemonURL: daemonAddr,
				Paths:     homePaths(".hermes/hooks/opencontext", ".opencontext/collectors/hermes-hooks/oc-hook.sh", ".hermes/config.yaml"),
				NextSteps: []string{"Restart Hermes with: hermes chat or hermes gateway"},
			}, dryRun, func() error {
				return installers.InstallHermes(daemonAddr)
			})
		},
	}
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://localhost:6060", "OpenContext daemon base URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview hook files and config changes without writing files")
	return cmd
}

func buildHermesUninstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove OpenContext gateway hook from Hermes Agent",
		Long:  `Removes the ~/.hermes/hooks/opencontext/ directory.`,
		Example: `  oc collector hermes uninstall
  oc collector hermes uninstall --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSideEffectCommand(sideEffectResult{
				Status:    "uninstalled",
				Action:    "uninstall",
				Collector: "hermes",
				DryRun:    dryRun,
				Paths:     homePaths(".hermes/hooks/opencontext", ".opencontext/collectors/hermes-hooks/oc-hook.sh"),
			}, dryRun, installers.UninstallHermes)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removed files without writing changes")
	return cmd
}

// ── Chrome browser collector ──────────────────────────────────────────────────

type browserChromeInstallResult struct {
	Status        string   `json:"status"`
	SourcePath    string   `json:"source_path"`
	ExtensionPath string   `json:"extension_path"`
	DaemonURL     string   `json:"daemon_url"`
	DryRun        bool     `json:"dry_run"`
	NextSteps     []string `json:"next_steps"`
}

func buildBrowserFirefoxCollectorCmd() *cobra.Command {
	browser := &cobra.Command{
		Use:     "browser-firefox",
		Aliases: []string{"firefox"},
		Short:   "Firefox browser extension collector commands",
		Long: `Manage the Firefox Manifest V3 browser collector.

The command prepares an unpacked extension directory. Firefox requires
the user to load the extension from about:debugging because browsers do
not allow silent installation of unpacked extensions.`,
	}
	browser.AddCommand(buildBrowserFirefoxInstallCmd())
	return browser
}

func buildBrowserFirefoxInstallCmd() *cobra.Command {
	var sourcePath string
	var targetPath string
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Prepare the Firefox extension collector for manual loading",
		Long: `Copies the Firefox browser collector extension to a stable OpenContext
directory and prints the exact Firefox UI steps the user must complete.

This is idempotent and safe to rerun.`,
		Example: `  oc collector browser-firefox install
  oc collector browser-firefox install --format json
  oc collector browser-firefox install --dry-run --format json
  oc collector browser-firefox install --source ./collectors/browser/firefox`,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := installBrowserFirefoxCollector(sourcePath, targetPath, daemonAddr, dryRun)
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(result)
			}
			if result.DryRun {
				fmt.Println("Firefox browser collector dry run.")
			} else {
				fmt.Println("Firefox browser collector prepared.")
			}
			fmt.Printf("  source:    %s\n", result.SourcePath)
			fmt.Printf("  extension: %s\n", result.ExtensionPath)
			fmt.Printf("  daemon:    %s\n", result.DaemonURL)
			fmt.Println("\nAsk the user to complete these Firefox steps:")
			for i, step := range result.NextSteps {
				fmt.Printf("  %d. %s\n", i+1, step)
			}
			return nil
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&sourcePath, "source", "", "source extension directory (default: auto-detect collectors/browser/firefox)")
	cmd.Flags().StringVar(&targetPath, "target", filepath.Join(home, ".opencontext", "collectors", "browser", "firefox"), "target extension directory")
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://127.0.0.1:6060", "OpenContext daemon base URL shown in extension options")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview copy and browser steps without writing files")
	return cmd
}

func installBrowserFirefoxCollector(sourcePath, targetPath, daemonAddr string, dryRun bool) (*browserChromeInstallResult, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	targetPath = expandHome(strings.TrimSpace(targetPath))
	if targetPath == "" {
		return nil, fmt.Errorf("--target is required")
	}
	if sourcePath == "" {
		var err error
		sourcePath, err = findBrowserFirefoxSource()
		if err != nil {
			return nil, err
		}
	} else {
		sourcePath = expandHome(sourcePath)
	}
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return nil, fmt.Errorf("resolve target path: %w", err)
	}
	if err := validateFirefoxExtensionDir(sourcePath); err != nil {
		return nil, err
	}
	if !dryRun {
		if err := copyDir(sourcePath, targetPath); err != nil {
			return nil, fmt.Errorf("copy extension files: %w", err)
		}
	}
	return &browserChromeInstallResult{
		Status:        "ready",
		SourcePath:    sourcePath,
		ExtensionPath: targetPath,
		DaemonURL:     daemonAddr,
		DryRun:        dryRun,
		NextSteps: []string{
			"Open about:debugging#/runtime/this-firefox.",
			"Click Load Temporary Add-on and select the manifest.json in the extension directory.",
			"Alternatively: open about:addons, click the gear icon, Install Add-on from file.",
			"Click the extension icon in the toolbar and set Daemon URL to " + daemonAddr + ".",
			"Click Send Test Event, then run: oc event list --source browser --since 10m.",
		},
	}, nil
}

func findBrowserFirefoxSource() (string, error) {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "collectors", "browser", "firefox"),
			filepath.Join(wd, "..", "collectors", "browser", "firefox"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "collectors", "browser", "firefox"),
			filepath.Join(base, "..", "collectors", "browser", "firefox"),
			filepath.Join(base, "..", "..", "collectors", "browser", "firefox"),
		)
	}
	for _, candidate := range candidates {
		if validateFirefoxExtensionDir(candidate) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find collectors/browser/firefox; pass --source or clone https://github.com/ohmyctx/opencontext")
}

func validateFirefoxExtensionDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read Firefox extension source %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Firefox extension source is not a directory: %s", path)
	}
	manifest := filepath.Join(path, "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf("Firefox extension source missing manifest.json at %s", manifest)
	}
	return nil
}

func buildBrowserChromeCollectorCmd() *cobra.Command {
	browser := &cobra.Command{
		Use:     "browser-chrome",
		Aliases: []string{"chrome"},
		Short:   "Chrome browser extension collector commands",
		Long: `Manage the Chrome Manifest V3 browser collector.

The command prepares an unpacked extension directory. Chrome still requires
the user to load that directory from chrome://extensions because browsers do
not allow silent installation of unpacked extensions.`,
	}
	browser.AddCommand(buildBrowserChromeInstallCmd())
	return browser
}

func buildBrowserChromeInstallCmd() *cobra.Command {
	var sourcePath string
	var targetPath string
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Prepare the Chrome extension collector for manual loading",
		Long: `Copies the Chrome browser collector extension to a stable OpenContext
directory and prints the exact Chrome UI steps the user must complete.

This is idempotent and safe to rerun. It does not silently change Chrome
policy or browser profiles.`,
		Example: `  oc collector browser-chrome install
  oc collector browser-chrome install --format json
  oc collector browser-chrome install --dry-run --format json
  oc collector browser-chrome install --source ./collectors/browser/chrome`,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := installBrowserChromeCollector(sourcePath, targetPath, daemonAddr, dryRun)
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(result)
			}
			if result.DryRun {
				fmt.Println("Chrome browser collector dry run.")
			} else {
				fmt.Println("Chrome browser collector prepared.")
			}
			fmt.Printf("  source:    %s\n", result.SourcePath)
			fmt.Printf("  extension: %s\n", result.ExtensionPath)
			fmt.Printf("  daemon:    %s\n", result.DaemonURL)
			fmt.Println("\nAsk the user to complete these Chrome steps:")
			for i, step := range result.NextSteps {
				fmt.Printf("  %d. %s\n", i+1, step)
			}
			return nil
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&sourcePath, "source", "", "source extension directory (default: auto-detect collectors/browser/chrome)")
	cmd.Flags().StringVar(&targetPath, "target", filepath.Join(home, ".opencontext", "collectors", "browser", "chrome"), "target extension directory")
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://127.0.0.1:6060", "OpenContext daemon base URL shown in extension options")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview copy and browser steps without writing files")
	return cmd
}

func installBrowserChromeCollector(sourcePath, targetPath, daemonAddr string, dryRun bool) (*browserChromeInstallResult, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	targetPath = expandHome(strings.TrimSpace(targetPath))
	if targetPath == "" {
		return nil, fmt.Errorf("--target is required")
	}
	if sourcePath == "" {
		var err error
		sourcePath, err = findBrowserChromeSource()
		if err != nil {
			return nil, err
		}
	} else {
		sourcePath = expandHome(sourcePath)
	}
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return nil, fmt.Errorf("resolve target path: %w", err)
	}
	if err := validateChromeExtensionDir(sourcePath); err != nil {
		return nil, err
	}
	if !dryRun {
		if err := copyDir(sourcePath, targetPath); err != nil {
			return nil, fmt.Errorf("copy extension files: %w", err)
		}
	}
	return &browserChromeInstallResult{
		Status:        "ready",
		SourcePath:    sourcePath,
		ExtensionPath: targetPath,
		DaemonURL:     daemonAddr,
		DryRun:        dryRun,
		NextSteps: []string{
			"Open chrome://extensions.",
			"Enable Developer mode.",
			"Click Load unpacked.",
			"Select " + targetPath + ".",
			"Open the OpenContext extension options and set Daemon URL to " + daemonAddr + ".",
			"Click Send Test Event, then run: oc event list --source browser --since 10m.",
		},
	}, nil
}

func findBrowserChromeSource() (string, error) {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "collectors", "browser", "chrome"),
			filepath.Join(wd, "..", "collectors", "browser", "chrome"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "collectors", "browser", "chrome"),
			filepath.Join(base, "..", "collectors", "browser", "chrome"),
			filepath.Join(base, "..", "..", "collectors", "browser", "chrome"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".opencontext", "collectors", "opencontext", "collectors", "browser", "chrome"),
		)
	}
	for _, candidate := range candidates {
		if validateChromeExtensionDir(candidate) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find collectors/browser/chrome; pass --source or clone https://github.com/ohmyctx/opencontext")
}

func validateChromeExtensionDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read Chrome extension source %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Chrome extension source is not a directory: %s", path)
	}
	manifest := filepath.Join(path, "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf("Chrome extension source missing manifest.json at %s", manifest)
	}
	return nil
}

func buildBrowserEdgeCollectorCmd() *cobra.Command {
	browser := &cobra.Command{
		Use:     "browser-edge",
		Aliases: []string{"edge"},
		Short:   "Edge browser extension collector commands",
		Long: `Manage the Edge Manifest V3 browser collector (same codebase as Chrome).

The command prepares an unpacked extension directory. Edge requires
the user to load that directory from edge://extensions because browsers do
not allow silent installation of unpacked extensions.`,
	}
	browser.AddCommand(buildBrowserEdgeInstallCmd())
	return browser
}

func buildBrowserEdgeInstallCmd() *cobra.Command {
	var sourcePath string
	var targetPath string
	var daemonAddr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Prepare the Edge extension collector for manual loading",
		Long: `Copies the Edge browser collector extension to a stable OpenContext
directory and prints the exact Edge UI steps the user must complete.

This is idempotent and safe to rerun.`,
		Example: `  oc collector browser-edge install
  oc collector browser-edge install --format json
  oc collector browser-edge install --dry-run --format json
  oc collector browser-edge install --source ./collectors/browser/edge`,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := installBrowserEdgeCollector(sourcePath, targetPath, daemonAddr, dryRun)
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(result)
			}
			if result.DryRun {
				fmt.Println("Edge browser collector dry run.")
			} else {
				fmt.Println("Edge browser collector prepared.")
			}
			fmt.Printf("  source:    %s\n", result.SourcePath)
			fmt.Printf("  extension: %s\n", result.ExtensionPath)
			fmt.Printf("  daemon:    %s\n", result.DaemonURL)
			fmt.Println("\nAsk the user to complete these Edge steps:")
			for i, step := range result.NextSteps {
				fmt.Printf("  %d. %s\n", i+1, step)
			}
			return nil
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&sourcePath, "source", "", "source extension directory (default: auto-detect collectors/browser/edge)")
	cmd.Flags().StringVar(&targetPath, "target", filepath.Join(home, ".opencontext", "collectors", "browser", "edge"), "target extension directory")
	cmd.Flags().StringVar(&daemonAddr, "daemon", "http://127.0.0.1:6060", "OpenContext daemon base URL shown in extension options")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview copy and browser steps without writing files")
	return cmd
}

func installBrowserEdgeCollector(sourcePath, targetPath, daemonAddr string, dryRun bool) (*browserChromeInstallResult, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	targetPath = expandHome(strings.TrimSpace(targetPath))
	if targetPath == "" {
		return nil, fmt.Errorf("--target is required")
	}
	if sourcePath == "" {
		var err error
		sourcePath, err = findBrowserEdgeSource()
		if err != nil {
			return nil, err
		}
	} else {
		sourcePath = expandHome(sourcePath)
	}
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return nil, fmt.Errorf("resolve target path: %w", err)
	}
	if err := validateChromeExtensionDir(sourcePath); err != nil {
		return nil, err
	}
	if !dryRun {
		if err := copyDir(sourcePath, targetPath); err != nil {
			return nil, fmt.Errorf("copy extension files: %w", err)
		}
	}
	return &browserChromeInstallResult{
		Status:        "ready",
		SourcePath:    sourcePath,
		ExtensionPath: targetPath,
		DaemonURL:     daemonAddr,
		DryRun:        dryRun,
		NextSteps: []string{
			"Open edge://extensions.",
			"Enable Developer mode.",
			"Click Load unpacked.",
			"Select " + targetPath + ".",
			"Open the OpenContext extension options and set Daemon URL to " + daemonAddr + ".",
			"Click Send Test Event, then run: oc event list --source browser --since 10m.",
		},
	}, nil
}

func findBrowserEdgeSource() (string, error) {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "collectors", "browser", "edge"),
			filepath.Join(wd, "..", "collectors", "browser", "edge"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "collectors", "browser", "edge"),
			filepath.Join(base, "..", "collectors", "browser", "edge"),
			filepath.Join(base, "..", "..", "collectors", "browser", "edge"),
		)
	}
	for _, candidate := range candidates {
		if validateChromeExtensionDir(candidate) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find collectors/browser/edge; pass --source or clone https://github.com/ohmyctx/opencontext")
}

func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// ── shell helpers ─────────────────────────────────────────────────────────────

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
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

func buildGitHookEvent(hook, repo, oldRef, newRef, flag, remote, remoteURL string, sensitivity int, stdin io.Reader) (*event.ActivityEvent, error) {
	repoRoot := gitOutput(repo, "rev-parse", "--show-toplevel")
	if repoRoot == "" {
		return nil, fmt.Errorf("not a git repository: %s", repo)
	}
	branch := gitOutput(repoRoot, "branch", "--show-current")
	if branch == "" {
		branch = gitOutput(repoRoot, "rev-parse", "--short", "HEAD")
	}
	repoName := filepath.Base(repoRoot)
	labels := map[string]string{
		"repo": repoName,
	}
	if branch != "" {
		labels["branch"] = branch
	}
	payload := map[string]any{
		"repo_path": repoRoot,
	}
	sens := event.SensitivityLevel(sensitivity)
	if sens < event.SensitivityL1 || sens > event.SensitivityL2 {
		sens = event.SensitivityL2
	}

	switch hook {
	case "post-commit":
		hash := gitOutput(repoRoot, "rev-parse", "--short", "HEAD")
		author := gitOutput(repoRoot, "show", "-s", "--format=%an", "HEAD")
		message := gitOutput(repoRoot, "show", "-s", "--format=%s", "HEAD")
		if author != "" {
			labels["author"] = author
		}
		if hash != "" {
			payload["hash"] = hash
		}
		if sens >= event.SensitivityL2 && message != "" {
			payload["message"] = message
		}
		addGitShortStat(repoRoot, payload)
		return &event.ActivityEvent{
			Ts:          time.Now().UnixMilli(),
			Source:      event.SourceGit,
			Type:        event.EventTypeCommit,
			Sensitivity: sens,
			Labels:      labels,
			Payload:     payload,
		}, nil
	case "post-checkout":
		if flag != "1" || oldRef == newRef || newRef == "" {
			return nil, nil
		}
		payload["from"] = shortGitHash(oldRef)
		payload["to"] = branch
		if newRef != "" {
			payload["to_hash"] = shortGitHash(newRef)
		}
		return &event.ActivityEvent{
			Ts:          time.Now().UnixMilli(),
			Source:      event.SourceGit,
			Type:        event.EventTypeBranchSwitch,
			Sensitivity: event.SensitivityL1,
			Labels:      labels,
			Payload:     payload,
		}, nil
	case "post-merge":
		hash := gitOutput(repoRoot, "rev-parse", "--short", "HEAD")
		message := gitOutput(repoRoot, "show", "-s", "--format=%s", "HEAD")
		if hash != "" {
			payload["hash"] = hash
		}
		if sens >= event.SensitivityL2 && message != "" {
			payload["message"] = message
		}
		return &event.ActivityEvent{
			Ts:          time.Now().UnixMilli(),
			Source:      event.SourceGit,
			Type:        event.EventTypeMerge,
			Sensitivity: sens,
			Labels:      labels,
			Payload:     payload,
		}, nil
	case "pre-push":
		refs := readPrePushRefs(stdin)
		if remote != "" {
			labels["remote"] = remote
			payload["remote"] = remote
		}
		if remoteURL != "" {
			payload["remote_url"] = remoteURL
		}
		payload["phase"] = "pre_push"
		payload["ref_count"] = len(refs)
		if len(refs) > 0 {
			payload["refs"] = refs
		}
		return &event.ActivityEvent{
			Ts:          time.Now().UnixMilli(),
			Source:      event.SourceGit,
			Type:        event.EventTypePush,
			Sensitivity: event.SensitivityL1,
			Labels:      labels,
			Payload:     payload,
		}, nil
	default:
		return nil, nil
	}
}

func gitOutput(repo string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func addGitShortStat(repo string, payload map[string]any) {
	stat := gitOutput(repo, "show", "--shortstat", "--format=", "HEAD")
	if stat == "" {
		return
	}
	for _, part := range strings.Split(stat, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(fields[1], "file"):
			payload["files_changed"] = n
		case strings.HasPrefix(fields[1], "insertion"):
			payload["insertions"] = n
		case strings.HasPrefix(fields[1], "deletion"):
			payload["deletions"] = n
		}
	}
}

func readPrePushRefs(r io.Reader) []map[string]string {
	var refs []map[string]string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		refs = append(refs, map[string]string{
			"local_ref":  fields[0],
			"local_sha":  shortGitHash(fields[1]),
			"remote_ref": fields[2],
			"remote_sha": shortGitHash(fields[3]),
		})
	}
	return refs
}

func shortGitHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ── oc schema ────────────────────────────────────────────────────────────────

type commandSchema struct {
	Command         string       `json:"command"`
	Use             string       `json:"use"`
	Description     string       `json:"description"`
	Aliases         []string     `json:"aliases,omitempty"`
	SideEffect      bool         `json:"side_effect"`
	Destructive     bool         `json:"destructive"`
	DryRunSupported bool         `json:"dry_run_supported"`
	Flags           []flagSchema `json:"flags,omitempty"`
	Subcommands     []string     `json:"subcommands,omitempty"`
	Examples        []string     `json:"examples,omitempty"`
}

type flagSchema struct {
	Name        string   `json:"name"`
	Shorthand   string   `json:"shorthand,omitempty"`
	Type        string   `json:"type"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`
}

func buildSchemaCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema [command...]",
		Short: "Print agent-readable CLI command schema",
		Long: `Print a JSON description of the oc command tree or a single command.

Agents should prefer this command over scraping help text when they need
available subcommands, flags, defaults, value domains, and examples.`,
		Example: `  oc schema
  oc schema collector browser-chrome install
  oc schema event list
  oc schema subscription list`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := findCommandForSchema(root, args)
			if err != nil {
				return err
			}
			return printJSON(buildCommandSchema(target))
		},
	}
	return cmd
}

func findCommandForSchema(root *cobra.Command, path []string) (*cobra.Command, error) {
	current := root
	for _, part := range path {
		found := false
		for _, child := range current.Commands() {
			if child.Hidden {
				continue
			}
			if child.Name() == part || containsString(child.Aliases, part) {
				current = child
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown command path %q; run: oc schema", strings.Join(path, " "))
		}
	}
	return current, nil
}

func buildCommandSchema(cmd *cobra.Command) commandSchema {
	s := commandSchema{
		Command:         commandPath(cmd),
		Use:             cmd.UseLine(),
		Description:     strings.TrimSpace(cmd.Short),
		Aliases:         cmd.Aliases,
		SideEffect:      annotationBool(cmd, "side_effect") || commandHasFlag(cmd, "dry-run"),
		Destructive:     annotationBool(cmd, "destructive") || cmd.Name() == "uninstall" || cmd.Name() == "clear",
		DryRunSupported: commandHasFlag(cmd, "dry-run"),
		Flags:           collectFlagSchemas(cmd),
		Examples:        splitExamples(cmd.Example),
	}
	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}
		s.Subcommands = append(s.Subcommands, child.Name())
	}
	sort.Strings(s.Subcommands)
	return s
}

func markSideEffect(cmd *cobra.Command, destructive bool) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["side_effect"] = "true"
	if destructive {
		cmd.Annotations["destructive"] = "true"
	}
	return cmd
}

func annotationBool(cmd *cobra.Command, key string) bool {
	if cmd == nil || cmd.Annotations == nil {
		return false
	}
	return cmd.Annotations[key] == "true"
}

func commandHasFlag(cmd *cobra.Command, name string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Flags().Lookup(name) != nil || c.PersistentFlags().Lookup(name) != nil || c.InheritedFlags().Lookup(name) != nil {
			return true
		}
	}
	return false
}

func collectFlagSchemas(cmd *cobra.Command) []flagSchema {
	flags := []flagSchema{}
	seen := map[string]bool{}
	addFlagSet := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Hidden || seen[f.Name] {
				return
			}
			seen[f.Name] = true
			flags = append(flags, flagSchema{
				Name:        "--" + f.Name,
				Shorthand:   shorthandFlag(f.Shorthand),
				Type:        f.Value.Type(),
				Default:     f.DefValue,
				Description: f.Usage,
				Required:    hasAnnotation(f, cobra.BashCompOneRequiredFlag),
				Enum:        enumFromUsage(f.Usage),
			})
		})
	}
	addFlags := func(c *cobra.Command) {
		addFlagSet(c.Flags())
		addFlagSet(c.PersistentFlags())
		addFlagSet(c.InheritedFlags())
	}
	addFlags(cmd)
	for c := cmd.Parent(); c != nil; c = c.Parent() {
		addFlagSet(c.PersistentFlags())
		addFlagSet(c.InheritedFlags())
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

func commandPath(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() != "" {
			parts = append(parts, c.Name())
		}
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, " ")
}

func splitExamples(example string) []string {
	example = strings.TrimSpace(example)
	if example == "" {
		return nil
	}
	lines := strings.Split(example, "\n")
	out := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func shorthandFlag(s string) string {
	if s == "" {
		return ""
	}
	return "-" + s
}

func hasAnnotation(f *pflag.Flag, key string) bool {
	values, ok := f.Annotations[key]
	return ok && len(values) > 0
}

func enumFromUsage(usage string) []string {
	for _, marker := range []string{"json|table", "debug|info|warn|error"} {
		if strings.Contains(usage, marker) {
			return strings.Split(marker, "|")
		}
	}
	return nil
}

func buildInjectHermesCmd() *cobra.Command {
	var (
		memoryPath string
		header     string
		configFile string
		dryRun     bool
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
		Example: `  oc memory target add hermes
  oc memory target add hermes --memory ~/.hermes/memories/MEMORY.md
  oc memory target add hermes --header "## Recent Dev Activity"
  oc memory target add hermes --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInjectTarget("hermes", memoryPath, header, configFile, dryRun)
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&memoryPath, "memory", filepath.Join(home, ".hermes", "memories", "MEMORY.md"), "path to Hermes MEMORY.md")
	cmd.Flags().StringVar(&header, "header", "## OpenContext — Recent Activity", "section heading inside the injected block")
	cmd.Flags().StringVar(&configFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview config patch without writing files")
	return markSideEffect(cmd, false)
}

func buildInjectOpenClawCmd() *cobra.Command {
	var (
		memoryPath string
		header     string
		configFile string
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "openclaw",
		Short: "Inject memory into OpenClaw workspace (~/.openclaw/workspace/MEMORY.md)",
		Long: `Adds OpenClaw's workspace MEMORY.md as an inject_target in your
OpenContext subscription config. After the next refresh cycle,
OpenContext will maintain an "OpenContext — Recent Activity" section
in that file.

	If your OpenClaw agents use a custom workspace path, pass it with --memory.`,
		Example: `  oc memory target add openclaw
  oc memory target add openclaw --memory ~/.openclaw/my-agent/MEMORY.md
  oc memory target add openclaw --dry-run --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInjectTarget("openclaw", memoryPath, header, configFile, dryRun)
		},
	}

	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&memoryPath, "memory", filepath.Join(home, ".openclaw", "workspace", "MEMORY.md"), "path to OpenClaw MEMORY.md")
	cmd.Flags().StringVar(&header, "header", "## OpenContext — Recent Activity", "section heading inside the injected block")
	cmd.Flags().StringVar(&configFile, "config", "", "OpenContext config file (default: ~/.opencontext/config.yaml)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview config patch without writing files")
	return markSideEffect(cmd, false)
}

func runInjectTarget(tool, memoryPath, header, configFile string, dryRun bool) error {
	resolvedConfig := configFile
	if resolvedConfig == "" {
		home, _ := os.UserHomeDir()
		resolvedConfig = filepath.Join(home, ".opencontext", "config.yaml")
	}
	result := sideEffectResult{
		Status:   "installed",
		Action:   "target_add",
		Resource: tool,
		DryRun:   dryRun,
		Paths:    compactStrings([]string{resolvedConfig, expandHome(memoryPath)}),
		NextSteps: []string{
			"Restart the OpenContext daemon or wait for config reload.",
			"Run: oc memory compile --format json",
		},
	}
	if dryRun {
		result.Status = "planned"
		if jsonOut {
			return printJSON(result)
		}
		printSideEffectPlan(result)
		return nil
	}
	if jsonOut {
		output, err := captureStdout(func() error {
			return installInjectTarget(tool, memoryPath, header, configFile)
		})
		if err != nil {
			return err
		}
		result.Output = strings.TrimSpace(output)
		return printJSON(result)
	}
	return installInjectTarget(tool, memoryPath, header, configFile)
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
		return fmt.Errorf("read config %s: %w\n\nRun 'oc daemon' first to create the default config.", configFile, err)
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
	fmt.Println("\nRestart the OpenContext daemon (or run: make restart) for changes to take effect.")
	fmt.Println("The memory section will be injected on the next refresh cycle.")
	return nil
}

// ── output helpers ────────────────────────────────────────────────────────────

type sideEffectResult struct {
	Status    string   `json:"status"`
	Action    string   `json:"action"`
	Resource  string   `json:"resource,omitempty"`
	Collector string   `json:"collector,omitempty"`
	DryRun    bool     `json:"dry_run"`
	Platform  string   `json:"platform,omitempty"`
	DaemonURL string   `json:"daemon_url,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	NextSteps []string `json:"next_steps,omitempty"`
	Output    string   `json:"output,omitempty"`
}

func runSideEffectCommand(result sideEffectResult, dryRun bool, action func() error) error {
	if dryRun {
		result.Status = "planned"
		if jsonOut {
			return printJSON(result)
		}
		printSideEffectPlan(result)
		return nil
	}
	if jsonOut {
		output, err := captureStdout(action)
		if err != nil {
			return err
		}
		result.Output = strings.TrimSpace(output)
		return printJSON(result)
	}
	return action()
}

func printSideEffectPlan(result sideEffectResult) {
	target := result.Collector
	if target == "" {
		target = result.Resource
	}
	if target == "" {
		target = result.Action
	}
	fmt.Printf("%s %s dry run.\n", titleCase(target), result.Action)
	if result.Platform != "" {
		fmt.Printf("  platform: %s\n", result.Platform)
	}
	if result.DaemonURL != "" {
		fmt.Printf("  daemon: %s\n", result.DaemonURL)
	}
	if len(result.Paths) > 0 {
		fmt.Println("  paths:")
		for _, p := range result.Paths {
			fmt.Printf("    %s\n", p)
		}
	}
	if len(result.NextSteps) > 0 {
		fmt.Println("  next steps:")
		for _, step := range result.NextSteps {
			fmt.Printf("    %s\n", step)
		}
	}
}

func captureStdout(action func() error) (string, error) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	actionErr := action()
	_ = w.Close()
	os.Stdout = orig
	out, readErr := io.ReadAll(r)
	_ = r.Close()
	if actionErr != nil {
		return "", actionErr
	}
	if readErr != nil {
		return "", readErr
	}
	return string(out), nil
}

func homePaths(parts ...string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return parts
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, filepath.Join(home, p))
	}
	return out
}

func shellCollectorPaths() []string {
	return homePaths(
		".opencontext/collectors/shell/hooks.zsh",
		".opencontext/collectors/shell/hooks.bash",
		".opencontext/collectors/shell/hooks.ps1",
		".zshrc",
		".bashrc",
		"Documents/PowerShell/Microsoft.PowerShell_profile.ps1",
		"Documents/WindowsPowerShell/Microsoft.PowerShell_profile.ps1",
	)
}

func gitCollectorPaths(repo string) ([]string, error) {
	repoRoot := gitOutput(repo, "rev-parse", "--show-toplevel")
	if repoRoot == "" {
		return nil, fmt.Errorf("not a git repository: %s", repo)
	}
	gitDir := gitOutput(repoRoot, "rev-parse", "--git-dir")
	if gitDir == "" {
		return nil, fmt.Errorf("resolve git dir: %s", repo)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	hooksDir := filepath.Join(gitDir, "hooks")
	return []string{
		filepath.Join(hooksDir, "post-commit"),
		filepath.Join(hooksDir, "post-checkout"),
		filepath.Join(hooksDir, "post-merge"),
		filepath.Join(hooksDir, "pre-push"),
		filepath.Join(hooksDir, ".opencontext-backup"),
	}, nil
}

func daemonInstallPaths(cfg service.Config) []string {
	paths := []string{
		cfg.BinaryPath,
		cfg.WorkDir,
		cfg.LogFile,
		filepath.Join(service.DefaultDataDir(), "daemon.json"),
	}
	if cfg.ConfigFile != "" {
		paths = append(paths, cfg.ConfigFile)
	}
	paths = append(paths, daemonPlatformPaths()...)
	return compactStrings(paths)
}

func daemonUninstallPaths() []string {
	return compactStrings(append([]string{filepath.Join(service.DefaultDataDir(), "daemon.json")}, daemonPlatformPaths()...))
}

func daemonPlatformPaths() []string {
	home, _ := os.UserHomeDir()
	switch {
	case runtime.GOOS == "darwin":
		return []string{filepath.Join(home, "Library", "LaunchAgents", "opencontext.plist")}
	case runtime.GOOS == "windows":
		return []string{filepath.Join(service.DefaultDataDir(), "opencontext-daemon.ps1")}
	case os.Getuid() == 0:
		return []string{"/etc/systemd/system/opencontext.service"}
	default:
		return []string{filepath.Join(home, ".config", "systemd", "user", "opencontext.service")}
	}
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func configureOutputMode() error {
	switch outputFormat {
	case "":
		if !stdoutIsTerminal() {
			jsonOut = true
		}
		return nil
	case "table":
		jsonOut = false
		return nil
	case "json":
		jsonOut = true
		return nil
	default:
		return fmt.Errorf("invalid --format %q; use json or table", outputFormat)
	}
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func printErrorJSON(err error) error {
	enc := json.NewEncoder(os.Stderr)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"error":      "command_failed",
		"message":    err.Error(),
		"retryable":  false,
		"suggestion": "Run `oc schema` or the command with `--help` to inspect valid arguments.",
	})
}

func buildEventSummary(e *event.ActivityEvent) string {
	if e.Source == event.SourceOS && e.Type == event.EventType("clipboard_copy") {
		if text := valueAsString(e.Payload["text"]); text != "" {
			return truncateSingleLine("copied: "+text, 80)
		}
		if files := valueAsString(e.Payload["files"]); files != "" {
			return truncateSingleLine("copied files: "+files, 80)
		}
		if contentType := valueAsString(e.Payload["content_type"]); contentType != "" {
			return "copied " + contentType
		}
	}
	if e.Source == event.SourceOS && e.Type == event.EventTypeScreenshot {
		if path := valueAsString(e.Payload["path"]); path != "" {
			return truncateSingleLine("screenshot: "+path, 80)
		}
		return "screenshot captured"
	}

	summary := firstEventString(e,
		"summary",
		"message",
		"command",
		"text",
		"title",
		"url",
		"control_name",
		"window_title",
		"app_name",
		"app",
		"project",
	)
	if summary == "" {
		summary = compactEventFields(e)
	}
	if summary == "" {
		summary = fmt.Sprintf("%s.%s", e.Source, e.Type)
	}
	if project := e.Labels["project"]; project != "" && !strings.Contains(summary, project) {
		summary = "[" + project + "] " + summary
	}
	if exit := e.Labels["exit_code"]; exit != "" && exit != "0" {
		summary += " (exit " + exit + ")"
	}
	return truncateSingleLine(summary, 80)
}

func firstEventString(e *event.ActivityEvent, keys ...string) string {
	for _, key := range keys {
		if v := valueAsString(e.Payload[key]); v != "" {
			return v
		}
		if v := e.Labels[key]; v != "" {
			return v
		}
	}
	return ""
}

func valueAsString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func compactEventFields(e *event.ActivityEvent) string {
	parts := []string{}
	for _, key := range sortedStringKeys(e.Labels) {
		if key == "project" || key == "exit_code" {
			continue
		}
		parts = append(parts, key+"="+e.Labels[key])
		if len(parts) >= 3 {
			return strings.Join(parts, " ")
		}
	}
	payload := map[string]string{}
	for key, val := range e.Payload {
		if s := valueAsString(val); s != "" {
			payload[key] = s
		}
	}
	for _, key := range sortedStringKeys(payload) {
		parts = append(parts, key+"="+payload[key])
		if len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func truncateSingleLine(s string, max int) string {
	for _, nl := range []string{"\r\n", "\n", "\r", "\t"} {
		s = strings.ReplaceAll(s, nl, " ")
	}
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func withResolvedCollectorVersions(in []registry.CollectorManifest) []registry.CollectorManifest {
	out := make([]registry.CollectorManifest, len(in))
	copy(out, in)
	for i := range out {
		out[i].Version = resolveCollectorVersion(out[i].Version)
	}
	return out
}

func resolveCollectorVersion(v string) string {
	if v == "bundled" {
		return version
	}
	return v
}

func sortSchemas(schemas []*event.EventTypeSchema) {
	sort.Slice(schemas, func(i, j int) bool {
		if schemas[i].Source == schemas[j].Source {
			return schemas[i].Type < schemas[j].Type
		}
		return schemas[i].Source < schemas[j].Source
	})
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
				return time.Now().Add(-time.Duration(val * float64(24*time.Hour))).UnixMilli()
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

func expandHome(path string) string {
	if path == "" || path == "~" {
		home, _ := os.UserHomeDir()
		if path == "~" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func printLastLines(path string, n int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read log %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := 0
	if n > 0 && len(lines) > n {
		start = len(lines) - n
	}
	for _, line := range lines[start:] {
		fmt.Println(line)
	}
	return nil
}

func followFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(line)
		}
		if err == io.EOF {
			time.Sleep(300 * time.Millisecond)
			reader.Reset(f)
			continue
		}
		if err != nil {
			return err
		}
	}
}
