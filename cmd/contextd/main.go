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
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"github.com/opencontext/opencontext/internal/compiler"
	"github.com/opencontext/opencontext/internal/ingester"
	"github.com/opencontext/opencontext/internal/policy"
	"github.com/opencontext/opencontext/internal/sessionizer"
	"github.com/opencontext/opencontext/internal/store"
	"github.com/opencontext/opencontext/internal/subscription"
	"github.com/opencontext/opencontext/pkg/event"
)

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

	// Policy filter
	policyCfg := policy.DefaultConfig()
	filter := policy.New(policyCfg)

	// Ingester
	ing := ingester.New(evStore, filter, log)
	ing.Start()
	defer ing.Stop()

	// Sessionizer + Compiler
	sess := sessionizer.New(sessionizer.DefaultConfig())
	comp := compiler.New(s, sess, log)
	if err := comp.BuildFromConfig(cfg.Subscriptions); err != nil {
		return fmt.Errorf("build compiler: %w", err)
	}

	// HTTP router
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(log))

	// Ingester routes
	ing.Mount(r)

	// Query + control routes
	r.Get("/api/v1/events", makeQueryHandler(evStore, log))
	r.Get("/api/v1/health", makeHealthHandler(evStore, version))
	r.Post("/api/v1/compile", makeCompileHandler(comp, cfg.Subscriptions, log))

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

		parseDuration := func(key string, defaultDur time.Duration) int64 {
			val := r.URL.Query().Get(key)
			if val == "" {
				return time.Now().Add(-defaultDur).UnixMilli()
			}
			if d, err := time.ParseDuration(val); err == nil {
				return time.Now().Add(-d).UnixMilli()
			}
			return 0
		}

		q.Since = parseDuration("since", 24*time.Hour)

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

// makeCompileHandler handles POST /api/v1/compile.
func makeCompileHandler(comp *compiler.Compiler, subs []subscription.Subscription, log *slog.Logger) http.HandlerFunc {
	subMap := map[string]*subscription.Subscription{}
	for i := range subs {
		subMap[subs[i].Name] = &subs[i]
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Subscription string `json:"subscription"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Subscription == "" {
			// Compile all subscriptions
			for _, sub := range subMap {
				go func(s *subscription.Subscription) {
					if err := comp.Run(context.Background(), s); err != nil {
						log.Error("compile failed", "subscription", s.Name, "err", err)
					}
				}(sub)
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

		go func() {
			if err := comp.Run(context.Background(), sub); err != nil {
				log.Error("compile failed", "subscription", sub.Name, "err", err)
			}
		}()

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
