// Package client provides a typed HTTP client for the OpenContext daemon API.
// Collectors use this package to push events without duplicating HTTP logic.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/ohmyctx/opencontext/pkg/event"
)

const defaultTimeout = 3 * time.Second

// Client is a typed HTTP client for the OpenContext daemon API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a Client targeting the given base URL (e.g. "http://localhost:6060").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// Push sends a single event to the OpenContext daemon.
func (c *Client) Push(ctx context.Context, e *event.ActivityEvent) (*event.PushResponse, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/events", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("OpenContext daemon returned %d: %s", resp.StatusCode, apiErr.Error)
	}

	var out event.PushResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// PushBatch sends multiple events in a single request.
func (c *Client) PushBatch(ctx context.Context, events []*event.ActivityEvent) (*event.BatchPushResponse, error) {
	body, err := json.Marshal(event.BatchPushRequest{Events: events})
	if err != nil {
		return nil, fmt.Errorf("marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/events/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("OpenContext daemon returned %d: %s", resp.StatusCode, apiErr.Error)
	}

	var out event.BatchPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// QueryEvents fetches events matching the given request parameters.
func (c *Client) QueryEvents(ctx context.Context, q *event.QueryRequest) (*event.QueryResponse, error) {
	params := url.Values{}
	if q.Source != "" {
		params.Set("source", string(q.Source))
	}
	if q.Project != "" {
		params.Set("project", q.Project)
	}
	if q.Since > 0 {
		params.Set("since_ts", fmt.Sprintf("%d", q.Since))
	}
	if q.Until > 0 {
		params.Set("until_ts", fmt.Sprintf("%d", q.Until))
	}
	if q.MaxSensitivity > 0 {
		params.Set("max_sensitivity", fmt.Sprintf("%d", q.MaxSensitivity))
	}
	if q.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.Query != "" {
		params.Set("q", q.Query)
	}

	rawURL := c.baseURL + "/api/v1/events"
	if len(params) > 0 {
		rawURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenContext daemon returned %d", resp.StatusCode)
	}

	var out event.QueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// TriggerCompile triggers the Memory Compiler for a named subscription.
func (c *Client) TriggerCompile(ctx context.Context, subscription string) error {
	body, _ := json.Marshal(map[string]string{"subscription": subscription})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/compile", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenContext daemon returned %d", resp.StatusCode)
	}
	return nil
}

// DeleteAllEvents removes all stored events from the OpenContext daemon.
func (c *Client) DeleteAllEvents(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/v1/events", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenContext daemon returned %d", resp.StatusCode)
	}
	return nil
}

// DeleteEventsBySource removes all stored events with the given source.
func (c *Client) DeleteEventsBySource(ctx context.Context, source string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/v1/events?source="+source, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenContext daemon returned %d", resp.StatusCode)
	}
	return nil
}

// Health returns the daemon health status.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/health", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenContext daemon returned %d", resp.StatusCode)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
