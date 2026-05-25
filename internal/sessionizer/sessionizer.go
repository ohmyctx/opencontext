// Package sessionizer groups raw ActivityEvents into ActivitySessions.
// It is purely rule-based — no LLM required — making it cheap to run on
// every Memory Compiler cycle.
//
// Grouping logic:
//  1. Partition events by project (from labels["project"])
//  2. Within each project, split into sessions at gaps > GapThreshold
//  3. Assign each session a unique ID and summary stub
package sessionizer

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/opencontext/opencontext/pkg/event"
	"github.com/opencontext/opencontext/pkg/session"
)

const defaultGapThreshold = 15 * time.Minute

// Config controls session splitting behaviour.
type Config struct {
	// GapThreshold is the minimum gap between two events that triggers a new session.
	GapThreshold time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{GapThreshold: defaultGapThreshold}
}

// Sessionizer produces ActivitySessions from a slice of ActivityEvents.
type Sessionizer struct {
	cfg Config
}

// New creates a Sessionizer with the given configuration.
func New(cfg Config) *Sessionizer {
	return &Sessionizer{cfg: cfg}
}

// Sessionize groups events into sessions. Events must all belong to the same
// window (e.g. "events since last compile"); the caller handles time filtering.
func (s *Sessionizer) Sessionize(events []*event.ActivityEvent) []*session.ActivitySession {
	if len(events) == 0 {
		return nil
	}

	// Sort by timestamp ascending for gap detection.
	sorted := make([]*event.ActivityEvent, len(events))
	copy(sorted, events)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ts < sorted[j].Ts })

	// Partition by project.
	byProject := map[string][]*event.ActivityEvent{}
	for _, e := range sorted {
		project := labelStr(e.Labels, "project")
		if project == "" {
			project = "_unknown"
		}
		byProject[project] = append(byProject[project], e)
	}

	var sessions []*session.ActivitySession
	gapMs := s.cfg.GapThreshold.Milliseconds()

	for project, evts := range byProject {
		// Split into sessions on gap.
		var group []*event.ActivityEvent
		for i, e := range evts {
			if i == 0 {
				group = append(group, e)
				continue
			}
			if e.Ts-evts[i-1].Ts > gapMs {
				if sess := buildSession(project, group); sess != nil {
					sessions = append(sessions, sess)
				}
				group = []*event.ActivityEvent{e}
			} else {
				group = append(group, e)
			}
		}
		if sess := buildSession(project, group); sess != nil {
			sessions = append(sessions, sess)
		}
	}

	// Sort sessions by start time.
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartTs < sessions[j].StartTs
	})

	return sessions
}

func buildSession(project string, events []*event.ActivityEvent) *session.ActivitySession {
	if len(events) == 0 {
		return nil
	}

	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}

	sess := &session.ActivitySession{
		ID:       uuid.Must(uuid.NewV7()).String(),
		StartTs:  events[0].Ts,
		EndTs:    events[len(events)-1].Ts,
		Project:  project,
		EventIDs: ids,
		Summary:  buildRuleSummary(project, events),
	}
	return sess
}

// buildRuleSummary creates a text summary without an LLM.
func buildRuleSummary(project string, events []*event.ActivityEvent) string {
	startTime := time.UnixMilli(events[0].Ts).Format("15:04")
	endTime := time.UnixMilli(events[len(events)-1].Ts).Format("15:04")

	counts := map[event.Source]int{}
	for _, e := range events {
		counts[e.Source]++
	}

	parts := []string{}
	if n := counts[event.SourceShell]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d shell cmd", n))
	}
	if n := counts[event.SourceGit]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d git event", n))
	}
	if n := counts[event.SourceIDE]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d IDE event", n))
	}
	if n := counts[event.SourceBrowser]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d browser event", n))
	}
	if n := counts[event.SourceOS]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d OS event", n))
	}

	summary := fmt.Sprintf("%s–%s  [%s]", startTime, endTime, project)
	if len(parts) > 0 {
		summary += " " + joinParts(parts)
	}
	return summary
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ", " + parts[i]
	}
	return result
}

func labelStr(labels map[string]string, key string) string {
	if labels == nil {
		return ""
	}
	return labels[key]
}
