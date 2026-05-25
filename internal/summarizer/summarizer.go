// Package summarizer converts ActivitySessions into human-readable Markdown
// summaries. The Summarizer interface is pluggable: swap in an LLM backend
// without touching the compiler or any other package.
package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opencontext/opencontext/pkg/event"
	"github.com/opencontext/opencontext/pkg/session"
)

// Summarizer converts a batch of sessions into Markdown text.
type Summarizer interface {
	// Summarize takes a list of sessions (with their events for context)
	// and returns a Markdown string describing the work done.
	Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}

// SummarizeRequest carries everything the Summarizer needs.
type SummarizeRequest struct {
	Sessions []*session.ActivitySession
	// Events keyed by event ID, used for payload context in LLM summaries.
	Events map[string]*event.ActivityEvent
	// Project hint for the prompt.
	Project string
}

// ── NoopSummarizer ────────────────────────────────────────────────────────────

// NoopSummarizer returns the rule-based summaries already attached to each
// session by the Sessionizer. Zero API cost.
type NoopSummarizer struct{}

func (NoopSummarizer) Summarize(_ context.Context, req SummarizeRequest) (string, error) {
	var sb strings.Builder
	for _, sess := range req.Sessions {
		sb.WriteString("- ")
		sb.WriteString(sess.Summary)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// ── LLMConfig ─────────────────────────────────────────────────────────────────

// LLMProvider identifies the LLM backend.
type LLMProvider string

const (
	ProviderOpenAI    LLMProvider = "openai"
	ProviderAnthropic LLMProvider = "anthropic"
	ProviderOllama    LLMProvider = "ollama"
)

// LLMConfig holds configuration for LLM-backed summarization.
type LLMConfig struct {
	Provider LLMProvider
	Model    string
	APIKey   string
	BaseURL  string // override endpoint (for Ollama or proxies)
}

// ── LLMSummarizer ─────────────────────────────────────────────────────────────

// LLMSummarizer calls an LLM API to produce narrative session summaries.
// It enriches the prompt with EventTypeSchema definitions so the model
// understands what each field means.
type LLMSummarizer struct {
	cfg    LLMConfig
	http   *http.Client
	log    *slog.Logger
	noop   NoopSummarizer
}

// NewLLMSummarizer creates an LLMSummarizer. Falls back to NoopSummarizer on
// API errors so the Memory Compiler always produces output.
func NewLLMSummarizer(cfg LLMConfig, log *slog.Logger) *LLMSummarizer {
	return &LLMSummarizer{
		cfg:  cfg,
		http: &http.Client{Timeout: 60 * time.Second},
		log:  log,
	}
}

func (s *LLMSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	systemPrompt := s.buildSystemPrompt(req)
	userMessage := s.buildUserMessage(req)

	var result string
	var err error

	switch s.cfg.Provider {
	case ProviderOpenAI:
		result, err = s.callOpenAI(ctx, systemPrompt, userMessage)
	case ProviderAnthropic:
		result, err = s.callAnthropic(ctx, systemPrompt, userMessage)
	case ProviderOllama:
		result, err = s.callOllama(ctx, systemPrompt, userMessage)
	default:
		err = fmt.Errorf("unknown LLM provider: %s", s.cfg.Provider)
	}

	if err != nil {
		s.log.Warn("LLM summarization failed, falling back to rule-based", "err", err)
		return s.noop.Summarize(ctx, req)
	}
	return result, nil
}

func (s *LLMSummarizer) buildSystemPrompt(req SummarizeRequest) string {
	// Gather schemas for all event types present in the sessions
	seenTypes := map[string]bool{}
	for _, sess := range req.Sessions {
		for _, id := range sess.EventIDs {
			if e, ok := req.Events[id]; ok {
				key := fmt.Sprintf("%s.%s", e.Source, e.Type)
				seenTypes[key] = true
			}
		}
	}

	var schemaDesc strings.Builder
	for key := range seenTypes {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			continue
		}
		schema := event.LookupSchema(event.Source(parts[0]), event.EventType(parts[1]))
		if schema == nil {
			continue
		}
		schemaDesc.WriteString(fmt.Sprintf("\n### %s\n%s\n", key, schema.Description))
		if len(schema.LabelDefs) > 0 {
			schemaDesc.WriteString("Labels: ")
			for k, def := range schema.LabelDefs {
				schemaDesc.WriteString(fmt.Sprintf("%s (%s); ", k, def.Description))
			}
			schemaDesc.WriteString("\n")
		}
		if len(schema.PayloadDefs) > 0 {
			schemaDesc.WriteString("Payload: ")
			for k, def := range schema.PayloadDefs {
				schemaDesc.WriteString(fmt.Sprintf("%s (%s); ", k, def.Description))
			}
			schemaDesc.WriteString("\n")
		}
	}

	return fmt.Sprintf(`You are a concise technical writer summarizing a developer's work sessions for an AI agent's memory file.

You will receive a JSON list of work sessions. Each session contains events with labels and payload fields.

Event type schemas (what each field means):
%s

Instructions:
- Write 1-3 bullet points per session in past tense
- Focus on WHAT was accomplished, not the mechanics of how
- Mention specific files, features, or errors when they appear in payloads
- Note any failures (exit_code != 0) and whether they were resolved
- Use the format: "- HH:MM-HH:MM  Brief description of work done"
- Output only the bullet points, no headers or extra text`, schemaDesc.String())
}

func (s *LLMSummarizer) buildUserMessage(req SummarizeRequest) string {
	type sessionJSON struct {
		StartTime string           `json:"start_time"`
		EndTime   string           `json:"end_time"`
		Project   string           `json:"project"`
		Events    []map[string]any `json:"events"`
	}

	var sessions []sessionJSON
	for _, sess := range req.Sessions {
		var evts []map[string]any
		for _, id := range sess.EventIDs {
			if e, ok := req.Events[id]; ok {
				evts = append(evts, map[string]any{
					"ts":      time.UnixMilli(e.Ts).Format("15:04:05"),
					"source":  e.Source,
					"type":    e.Type,
					"labels":  e.Labels,
					"payload": e.Payload,
				})
			}
		}
		sessions = append(sessions, sessionJSON{
			StartTime: time.UnixMilli(sess.StartTs).Format("15:04"),
			EndTime:   time.UnixMilli(sess.EndTs).Format("15:04"),
			Project:   sess.Project,
			Events:    evts,
		})
	}

	b, _ := json.MarshalIndent(sessions, "", "  ")
	return string(b)
}

// ── OpenAI ────────────────────────────────────────────────────────────────────

func (s *LLMSummarizer) callOpenAI(ctx context.Context, system, user string) (string, error) {
	baseURL := s.cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	body, _ := json.Marshal(map[string]any{
		"model": s.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens": 1024,
	})

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)

	return s.doRequest(req)
}

// ── Anthropic ─────────────────────────────────────────────────────────────────

func (s *LLMSummarizer) callAnthropic(ctx context.Context, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      s.cfg.Model,
		"max_tokens": 1024,
		"system":     system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	return s.doRequest(req)
}

// ── Ollama ────────────────────────────────────────────────────────────────────

func (s *LLMSummarizer) callOllama(ctx context.Context, system, user string) (string, error) {
	baseURL := s.cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	body, _ := json.Marshal(map[string]any{
		"model": s.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": false,
	})

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	return s.doRequest(req)
}

// doRequest executes an HTTP request and extracts the text content from
// the response. Handles both OpenAI and Anthropic response shapes.
func (s *LLMSummarizer) doRequest(req *http.Request) (string, error) {
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API returned %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}

	// OpenAI / Ollama shape: choices[0].message.content
	if choices, ok := raw["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				if content, ok := msg["content"].(string); ok {
					return content, nil
				}
			}
		}
	}

	// Anthropic shape: content[0].text
	if contents, ok := raw["content"].([]any); ok && len(contents) > 0 {
		if block, ok := contents[0].(map[string]any); ok {
			if text, ok := block["text"].(string); ok {
				return text, nil
			}
		}
	}

	return "", fmt.Errorf("could not extract text from LLM response")
}
