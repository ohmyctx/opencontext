// Package compiler implements the Memory Compiler: the async background task
// that reads raw events, groups them into sessions, summarizes them with an
// optional LLM, and writes the result to a MemoryBackend.
//
// The Compiler runs on a schedule (cron or fixed interval) per subscription.
// It can also be triggered on demand via the POST /api/v1/compile API.
package compiler

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/opencontext/opencontext/internal/memory"
	"github.com/opencontext/opencontext/internal/sessionizer"
	"github.com/opencontext/opencontext/internal/store"
	"github.com/opencontext/opencontext/internal/subscription"
	"github.com/opencontext/opencontext/internal/summarizer"
	"github.com/opencontext/opencontext/pkg/event"
	"github.com/opencontext/opencontext/pkg/session"
)

// Compiler runs the memory compilation pipeline for a set of subscriptions.
type Compiler struct {
	store       *store.Store
	sessionizer *sessionizer.Sessionizer
	log         *slog.Logger
	backends    map[string]memory.Backend // key = subscription name
	summarizers map[string]summarizer.Summarizer
}

// New creates a Compiler. Call BuildFromConfig to populate backends and
// summarizers from subscription configuration.
func New(s *store.Store, sess *sessionizer.Sessionizer, log *slog.Logger) *Compiler {
	return &Compiler{
		store:       s,
		sessionizer: sess,
		log:         log,
		backends:    map[string]memory.Backend{},
		summarizers: map[string]summarizer.Summarizer{},
	}
}

// BuildFromConfig initialises backends and summarizers for all subscriptions.
func (c *Compiler) BuildFromConfig(subs []subscription.Subscription) error {
	for _, sub := range subs {
		// Memory backend
		switch sub.Memory.Backend {
		case subscription.BackendFile, "":
			path := sub.Memory.Path
			if path == "" {
				return fmt.Errorf("subscription %q: file backend requires memory.path", sub.Name)
			}
			c.backends[sub.Name] = memory.NewFileBackend(path)
		default:
			return fmt.Errorf("subscription %q: unknown memory backend %q", sub.Name, sub.Memory.Backend)
		}

		// Summarizer
		if llmCfg := sub.LLMSummarizerConfig(); llmCfg != nil {
			c.summarizers[sub.Name] = summarizer.NewLLMSummarizer(*llmCfg, c.log)
		} else {
			c.summarizers[sub.Name] = summarizer.NoopSummarizer{}
		}
	}
	return nil
}

// Run executes the compilation pipeline for the named subscription.
func (c *Compiler) Run(ctx context.Context, sub *subscription.Subscription) error {
	backend, ok := c.backends[sub.Name]
	if !ok {
		return fmt.Errorf("no backend registered for subscription %q", sub.Name)
	}
	smz := c.summarizers[sub.Name]

	c.log.Info("starting memory compile", "subscription", sub.Name)
	start := time.Now()

	// 1. Query recent events matching the subscription filter
	since := time.Now().Add(-7 * 24 * time.Hour).UnixMilli() // last 7 days
	events, err := c.queryEvents(ctx, sub, since)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	c.log.Debug("queried events", "count", len(events), "subscription", sub.Name)

	if len(events) == 0 {
		c.log.Info("no events found, skipping compile", "subscription", sub.Name)
		return nil
	}

	// 2. Group into sessions
	sessions := c.sessionizer.Sessionize(events)
	c.log.Debug("sessionized", "sessions", len(sessions), "subscription", sub.Name)

	// 3. Persist sessions
	if err := c.store.Sessions.Save(ctx, sessions); err != nil {
		c.log.Warn("failed to persist sessions", "err", err)
	}

	// 4. Build events index for LLM context
	eventIndex := make(map[string]*event.ActivityEvent, len(events))
	for _, e := range events {
		eventIndex[e.ID] = e
	}

	// 5. Classify sessions into memory tiers
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	weekStart := now.AddDate(0, 0, -7).UnixMilli()

	var hotSessions, warmSessions, coldSessions []*session.ActivitySession
	for _, s := range sessions {
		switch {
		case s.StartTs >= todayStart:
			hotSessions = append(hotSessions, s)
		case s.StartTs >= weekStart:
			warmSessions = append(warmSessions, s)
		default:
			coldSessions = append(coldSessions, s)
		}
	}

	// 6. Summarize each tier
	hotItems, err := c.summarizeTier(ctx, smz, eventIndex, hotSessions, session.TierHot, "Today")
	if err != nil {
		return err
	}
	warmItems, err := c.summarizeTier(ctx, smz, eventIndex, warmSessions, session.TierWarm, "This Week")
	if err != nil {
		return err
	}
	coldItems, err := c.summarizeTier(ctx, smz, eventIndex, coldSessions, session.TierCold, "History")
	if err != nil {
		return err
	}

	// 7. Build MemoryContent
	project := inferProject(sub)
	content := &session.MemoryContent{
		Project:   project,
		UpdatedAt: time.Now().UnixMilli(),
		Hot:       hotItems,
		Warm:      warmItems,
		Cold:      coldItems,
	}

	// 8. Write to backend
	if err := backend.Write(ctx, content); err != nil {
		return fmt.Errorf("write memory: %w", err)
	}

	c.log.Info("memory compile complete",
		"subscription", sub.Name,
		"sessions", len(sessions),
		"elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// queryEvents fetches events matching a subscription's filter.
func (c *Compiler) queryEvents(ctx context.Context, sub *subscription.Subscription, since int64) ([]*event.ActivityEvent, error) {
	// If subscription filters by projects, query each project separately and merge
	if len(sub.Filter.Projects) > 0 {
		var all []*event.ActivityEvent
		for _, proj := range sub.Filter.Projects {
			evts, err := c.store.Events.Query(ctx, &event.QueryRequest{
				Project:        proj,
				Since:          since,
				MaxSensitivity: sub.MaxSensitivity(),
				Limit:          5000,
			})
			if err != nil {
				return nil, err
			}
			all = append(all, evts...)
		}
		// Sort merged results by timestamp
		sort.Slice(all, func(i, j int) bool { return all[i].Ts < all[j].Ts })
		return all, nil
	}

	// No project filter — query all
	return c.store.Events.Query(ctx, &event.QueryRequest{
		Since:          since,
		MaxSensitivity: sub.MaxSensitivity(),
		Limit:          5000,
	})
}

// summarizeTier groups sessions into MemoryItems and summarizes them.
func (c *Compiler) summarizeTier(
	ctx context.Context,
	smz summarizer.Summarizer,
	eventIndex map[string]*event.ActivityEvent,
	sessions []*session.ActivitySession,
	tier session.MemoryTier,
	title string,
) ([]*session.MemoryItem, error) {
	if len(sessions) == 0 {
		return nil, nil
	}

	req := summarizer.SummarizeRequest{
		Sessions: sessions,
		Events:   eventIndex,
	}

	body, err := smz.Summarize(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("summarize %s tier: %w", title, err)
	}

	startTs := sessions[0].StartTs
	endTs := sessions[len(sessions)-1].EndTs

	sessionIDs := make([]string, len(sessions))
	for i, s := range sessions {
		sessionIDs[i] = s.ID
	}

	item := &session.MemoryItem{
		Tier:       tier,
		StartTs:    startTs,
		EndTs:      endTs,
		Title:      fmt.Sprintf("%s (%s–%s)", title, formatDate(startTs), formatDate(endTs)),
		Body:       strings.TrimSpace(body),
		SessionIDs: sessionIDs,
	}

	return []*session.MemoryItem{item}, nil
}

func inferProject(sub *subscription.Subscription) string {
	if len(sub.Filter.Projects) == 1 {
		return sub.Filter.Projects[0]
	}
	if len(sub.Filter.Projects) > 1 {
		return strings.Join(sub.Filter.Projects, ", ")
	}
	return sub.Name
}

func formatDate(tsMs int64) string {
	return time.UnixMilli(tsMs).Format("2006-01-02")
}
