// contextd is the OpenContext daemon. It accepts activity events from collectors,
// stores them in SQLite, and periodically compiles them into agent-readable memory files.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"github.com/yetanotherai/opencontext/internal/compiler"
	"github.com/yetanotherai/opencontext/internal/ingester"
	"github.com/yetanotherai/opencontext/internal/policy"
	"github.com/yetanotherai/opencontext/internal/sessionizer"
	"github.com/yetanotherai/opencontext/internal/store"
	"github.com/yetanotherai/opencontext/internal/subscription"
	"github.com/yetanotherai/opencontext/pkg/event"
)

// startPruner runs a daily job that deletes events older than retentionDays.
// retentionDays <= 0 is treated as the default (90 days).
func startPruner(ctx context.Context, es store.EventStore, retentionDays int, log *slog.Logger) {
	days := retentionDays
	if days <= 0 {
		days = 90
	}
	retention := time.Duration(days) * 24 * time.Hour
	log.Info("event pruner started", "retention_days", days)

	prune := func() {
		cutoff := time.Now().Add(-retention).UnixMilli()
		n, err := es.Prune(ctx, cutoff)
		if err != nil {
			log.Warn("prune failed", "err", err)
			return
		}
		if n > 0 {
			log.Info("pruned old events", "deleted", n, "retention_days", days)
		}
	}

	// Run once at startup so a restarted daemon cleans up immediately.
	prune()

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			prune()
		case <-ctx.Done():
			return
		}
	}
}

// startRawDumpScheduler runs the RawDumpRunner for each raw_dump subscription
// on its configured refresh interval. Runs until ctx is cancelled.
func startRawDumpScheduler(ctx context.Context, subs []subscription.Subscription, s *store.Store, log *slog.Logger) {
	runner := compiler.NewRawDumpRunner(s, log)

	for i := range subs {
		sub := &subs[i]
		if sub.Memory.Backend != subscription.BackendRawDump {
			continue
		}

		interval := sub.EffectiveRefreshInterval()
		log.Info("raw_dump scheduler started", "subscription", sub.Name, "interval", interval)

		go func(s *subscription.Subscription) {
			// Run once immediately on startup
			if err := runner.Run(ctx, s); err != nil {
				log.Warn("raw dump failed", "subscription", s.Name, "err", err)
			}

			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := runner.Run(ctx, s); err != nil {
						log.Warn("raw dump failed", "subscription", s.Name, "err", err)
					}
				case <-ctx.Done():
					return
				}
			}
		}(sub)
	}
}

var (
	cfgFile  string
	logLevel string
	version  = "0.1.0"
)

func main() {
	root := &cobra.Command{
		Use:   "contextd",
		Short: "OpenContext daemon — memory beyond the chat",
		RunE:  runDaemon,
	}

	root.Flags().StringVar(&cfgFile, "config", "", "config file (default: ~/.opencontext/config.yaml)")
	root.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runDaemon(cmd *cobra.Command, args []string) error {
	log := buildLogger(logLevel)

	// Load configuration
	cfg, err := subscription.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}

	log.Info("starting contextd", "version", version, "data_dir", cfg.DataDir, "addr", cfg.ListenAddr)

	// Open SQLite store
	evStore, sessStore, err := store.OpenSQLite(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer evStore.Close()

	s := &store.Store{Events: evStore, Sessions: sessStore}

	// Policy filter — honour the global max_sensitivity from config (defaults to L2)
	policyCfg := policy.DefaultConfig()
	if cfg.MaxSensitivity > 0 {
		policyCfg.MaxSensitivity = cfg.MaxSensitivity
	}
	filter := policy.New(policyCfg)

	// Ingester
	ing := ingester.New(evStore, filter, log)
	ing.Start()
	defer ing.Stop()

	// Sessionizer + Compiler (for LLM-based subscriptions)
	sess := sessionizer.New(sessionizer.DefaultConfig())
	comp := compiler.New(s, sess, log)
	if err := comp.BuildFromConfig(cfg.Subscriptions); err != nil {
		return fmt.Errorf("build compiler: %w", err)
	}

	// RawDumpRunner (for raw_dump subscriptions, zero-config)
	rawDump := compiler.NewRawDumpRunner(s, log)

	// HTTP router
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(log))

	// Ingester routes
	ing.Mount(r)
	ing.MountClaudeHook(r)
	ing.MountCodexHook(r)
	ing.MountCursorHook(r)
	ing.MountOpenCodeHook(r)

	// Query + control routes
	r.Get("/api/v1/events", makeQueryHandler(evStore, log))
	r.Delete("/api/v1/events", makeDeleteEventsHandler(evStore, log))
	r.Get("/api/v1/health", makeHealthHandler(evStore, version))
	r.Post("/api/v1/compile", makeCompileHandler(comp, rawDump, cfg.Subscriptions, log))

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start raw_dump scheduler and event pruner after ctx is ready.
	startRawDumpScheduler(ctx, cfg.Subscriptions, s, log)
	go startPruner(ctx, evStore, cfg.RetentionDays, log)

	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// makeQueryHandler handles GET /api/v1/events.
func makeQueryHandler(es store.EventStore, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := &event.QueryRequest{}

		if s := r.URL.Query().Get("source"); s != "" {
			q.Source = event.Source(s)
		}
		q.Project = r.URL.Query().Get("project")
		q.Query = r.URL.Query().Get("q")

		parseSince := func(key string, defaultDur time.Duration) int64 {
			val := r.URL.Query().Get(key)
			if val == "" {
				return time.Now().Add(-defaultDur).UnixMilli()
			}
			// First try parsing as duration string (e.g., "10m", "2h", "7d")
			if d, err := time.ParseDuration(val); err == nil {
				return time.Now().Add(-d).UnixMilli()
			}
			// Then try parsing as Unix timestamp in milliseconds
			if ts, err := strconv.ParseInt(val, 10, 64); err == nil && ts > 0 {
				return ts
			}
			return 0
		}

		q.Since = parseSince("since_ts", 24*time.Hour)

		if lim := r.URL.Query().Get("limit"); lim != "" {
			fmt.Sscanf(lim, "%d", &q.Limit)
		}

		events, err := es.Query(r.Context(), q)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		resp := event.QueryResponse{
			Events:    events,
			Total:     len(events),
			Truncated: len(events) == q.Limit,
		}
		if resp.Events == nil {
			resp.Events = []*event.ActivityEvent{}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// makeHealthHandler handles GET /api/v1/health.
func makeHealthHandler(es store.EventStore, ver string) http.HandlerFunc {
	start := time.Now()
	return func(w http.ResponseWriter, r *http.Request) {
		count, _ := es.Count(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "ok",
			"version":        ver,
			"uptime_seconds": int(time.Since(start).Seconds()),
			"events_stored":  count,
		})
	}
}

// makeDeleteEventsHandler handles DELETE /api/v1/events.
func makeDeleteEventsHandler(es store.EventStore, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source := r.URL.Query().Get("source")
		if source != "" {
			if err := es.DeleteBySource(r.Context(), source); err != nil {
				log.Warn("delete events by source failed", "source", source, "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "source": source})
			return
		}
		if err := es.DeleteAll(r.Context()); err != nil {
			log.Warn("delete events failed", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// makeCompileHandler handles POST /api/v1/compile.
func makeCompileHandler(comp *compiler.Compiler, rawDump *compiler.RawDumpRunner, subs []subscription.Subscription, log *slog.Logger) http.HandlerFunc {
	subMap := map[string]*subscription.Subscription{}
	for i := range subs {
		subMap[subs[i].Name] = &subs[i]
	}

	runSub := func(sub *subscription.Subscription) {
		if sub.Memory.Backend == subscription.BackendRawDump {
			if err := rawDump.Run(context.Background(), sub); err != nil {
				log.Error("raw dump failed", "subscription", sub.Name, "err", err)
			}
		} else {
			if err := comp.Run(context.Background(), sub); err != nil {
				log.Error("compile failed", "subscription", sub.Name, "err", err)
			}
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Subscription string `json:"subscription"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Subscription == "" {
			for _, sub := range subMap {
				go runSub(sub)
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "triggered", "subscription": "all"})
			return
		}

		sub, ok := subMap[req.Subscription]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("subscription %q not found", req.Subscription),
			})
			return
		}

		go runSub(sub)
		writeJSON(w, http.StatusOK, map[string]string{"status": "triggered", "subscription": req.Subscription})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func buildLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Debug("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"elapsed", time.Since(start).Round(time.Microsecond),
			)
		})
	}
}
