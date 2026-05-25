// Package session defines session and memory types. It has zero external
// dependencies; everything is pure data structures.
package session

// MemoryTier classifies how recent and granular a memory item is.
type MemoryTier int

const (
	// TierHot contains individual activity sessions from today/this week.
	TierHot MemoryTier = iota + 1
	// TierWarm contains project-level summaries from this month.
	TierWarm
	// TierCold contains thematic/conclusion records from older history.
	TierCold
)

// ActivitySession is a coherent chunk of user activity: consecutive events
// within the same project, separated by less than the session gap threshold.
// Produced by the Sessionizer.
type ActivitySession struct {
	ID       string   `json:"id"`        // UUIDv7
	StartTs  int64    `json:"start_ts"`  // Unix ms
	EndTs    int64    `json:"end_ts"`    // Unix ms
	Project  string   `json:"project"`   // inferred from event labels
	Topic    string   `json:"topic"`     // inferred by Summarizer (e.g., "implementing ingester")
	EventIDs []string `json:"event_ids"` // ordered list of ActivityEvent IDs
	Summary  string   `json:"summary"`   // markdown summary (rule-based or LLM)
}

// DurationMs returns session duration in milliseconds.
func (s *ActivitySession) DurationMs() int64 {
	return s.EndTs - s.StartTs
}

// MemoryItem is a rendered memory entry ready for writing to a MemoryBackend.
// It may represent a single session (Hot), a day summary (Warm), or a
// conclusion record (Cold).
type MemoryItem struct {
	ID         string     `json:"id"`
	Tier       MemoryTier `json:"tier"`
	Project    string     `json:"project"`
	StartTs    int64      `json:"start_ts"`
	EndTs      int64      `json:"end_ts"`
	Title      string     `json:"title"`   // one-line heading for memory.md
	Body       string     `json:"body"`    // markdown body
	SessionIDs []string   `json:"session_ids"`
}

// MemoryContent is the full structured output passed to a MemoryBackend.
// The backend decides how to serialize it (e.g., as memory.md).
type MemoryContent struct {
	Project   string        `json:"project"`
	UpdatedAt int64         `json:"updated_at"` // Unix ms
	OpenLoops []string      `json:"open_loops"` // unresolved items
	Hot       []*MemoryItem `json:"hot"`
	Warm      []*MemoryItem `json:"warm"`
	Cold      []*MemoryItem `json:"cold"`
}
