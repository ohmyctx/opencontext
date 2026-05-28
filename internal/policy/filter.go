// Package policy enforces sensitivity-based filtering of ActivityEvents.
// It is the privacy gatekeeper: events that exceed the allowed sensitivity
// level are dropped before reaching the EventStore.
package policy

import (
	"regexp"
	"strings"

	"github.com/ohmyctx/opencontext/pkg/event"
)

// Filter decides which events are allowed to be stored.
type Filter interface {
	// Allow returns true if the event should be stored.
	Allow(e *event.ActivityEvent) bool
}

// Config holds policy configuration.
type Config struct {
	// MaxSensitivity is the highest SensitivityLevel allowed to pass.
	// Events with Sensitivity > MaxSensitivity are dropped.
	MaxSensitivity event.SensitivityLevel

	// ExcludeSources is a set of sources to always drop.
	ExcludeSources map[event.Source]bool

	// ExcludeCommandPatterns is a list of regex patterns applied to the
	// "command" payload field for shell events. Matching events are dropped.
	ExcludeCommandPatterns []string

	compiledPatterns []*regexp.Regexp
}

// Compile pre-compiles regex patterns. Must be called before Use.
func (c *Config) Compile() error {
	c.compiledPatterns = nil
	for _, p := range c.ExcludeCommandPatterns {
		r, err := regexp.Compile(p)
		if err != nil {
			return err
		}
		c.compiledPatterns = append(c.compiledPatterns, r)
	}
	return nil
}

// DefaultConfig returns a sensible starting configuration.
// Allows up to L3 (clipboard, raw keys) — collectors gate individual event
// types at their own sensitivity level; the policy default is permissive.
func DefaultConfig() Config {
	cfg := Config{
		MaxSensitivity: event.SensitivityL3,
		ExcludeSources: map[event.Source]bool{},
		ExcludeCommandPatterns: []string{
			`^\s`,                              // leading space = shell privacy convention
			`^(history|exit)$`,                 // always drop: reveals unrelated commands or ends session
			`^(ls|ll|la|pwd|cd|clear|reset)$`,  // bare no-arg commands with no context value
		},
	}
	_ = cfg.Compile()
	return cfg
}

// PolicyFilter is the standard Filter implementation.
type PolicyFilter struct {
	cfg Config
}

// New creates a PolicyFilter from the given config.
// Call cfg.Compile() before passing to New, or use DefaultConfig().
func New(cfg Config) *PolicyFilter {
	return &PolicyFilter{cfg: cfg}
}

// Allow implements Filter.
func (f *PolicyFilter) Allow(e *event.ActivityEvent) bool {
	if e.Sensitivity > f.cfg.MaxSensitivity {
		return false
	}
	if f.cfg.ExcludeSources[e.Source] {
		return false
	}
	if e.Source == event.SourceShell && e.Type == event.EventTypeCommand {
		cmd := strings.TrimSpace(payloadStr(e.Payload, "command"))
		if cmd == "" {
			return false
		}
		for _, r := range f.cfg.compiledPatterns {
			if r.MatchString(cmd) {
				return false
			}
		}
	}
	return true
}

func payloadStr(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
