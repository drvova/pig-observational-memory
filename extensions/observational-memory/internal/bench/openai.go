package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The usage roles.
const (
	RoleObserver  = "observer"
	RoleReflector = "reflector"
	RoleDropper   = "dropper"
	RoleActor     = "actor"
	RoleJudge     = "judge"
	RoleWorker    = "worker"
)

// ChatMessage is one OpenAI chat-completions message.
type ChatMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// Text is the message content, "" when null.
func (m ChatMessage) Text() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

// ReasoningText is the reasoning trace under either field name providers use.
func (m ChatMessage) ReasoningText() string {
	if m.ReasoningContent != "" {
		return m.ReasoningContent
	}
	return m.Reasoning
}

// Text builds a content pointer.
func Text(s string) *string { return &s }

// ToolCall is an assistant tool call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall is a tool call's function and JSON-encoded arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec is a function tool offered to the model.
type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec describes a function tool.
type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// ChatRequest is a chat-completions request.
type ChatRequest struct {
	Model           string        `json:"model"`
	Messages        []ChatMessage `json:"messages"`
	Tools           []ToolSpec    `json:"tools,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

// ChatUsage is a response's token usage. Providers report reasoning tokens
// either top-level or under completion_tokens_details.
type ChatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	ReasoningTokens     int `json:"reasoning_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// Reasoning is the reasoning-token count wherever it was reported.
func (u ChatUsage) Reasoning() int {
	if u.ReasoningTokens > 0 {
		return u.ReasoningTokens
	}
	if u.CompletionTokensDetails != nil {
		return u.CompletionTokensDetails.ReasoningTokens
	}
	return 0
}

// Cached is the prompt-cache hit count, when reported.
func (u ChatUsage) Cached() int {
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens
	}
	return 0
}

// ChatChoice is one completion choice.
type ChatChoice struct {
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatResponse is a chat-completions response.
type ChatResponse struct {
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

// RoleUsage is the accumulated usage of one role.
type RoleUsage struct {
	Calls            int   `json:"calls"`
	FailedCalls      int   `json:"failedCalls"`
	Retries          int   `json:"retries"`
	PromptTokens     int   `json:"promptTokens"`
	CompletionTokens int   `json:"completionTokens"`
	ReasoningTokens  int   `json:"reasoningTokens"`
	CachedTokens     int   `json:"cachedTokens"`
	LatencyMs        int64 `json:"latencyMs"`
}

// Add accumulates other into u.
func (u *RoleUsage) Add(other RoleUsage) {
	u.Calls += other.Calls
	u.FailedCalls += other.FailedCalls
	u.Retries += other.Retries
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.CachedTokens += other.CachedTokens
	u.LatencyMs += other.LatencyMs
}

// Usage is token usage by role.
type Usage map[string]RoleUsage

// Merge adds other into u.
func (u Usage) Merge(other Usage) {
	for role, usage := range other {
		total := u[role]
		total.Add(usage)
		u[role] = total
	}
}

// Total sums every role.
func (u Usage) Total() RoleUsage {
	total := RoleUsage{}
	for _, usage := range u {
		total.Add(usage)
	}
	return total
}

// Meter accumulates usage safely across goroutines.
type Meter struct {
	mu    sync.Mutex
	usage Usage
}

// NewMeter starts a meter, optionally from saved usage.
func NewMeter(initial Usage) *Meter {
	m := &Meter{usage: Usage{}}
	m.usage.Merge(initial)
	return m
}

func (m *Meter) record(role string, add RoleUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.usage[role]
	total.Add(add)
	m.usage[role] = total
}

// Snapshot copies the current usage.
func (m *Meter) Snapshot() Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Usage{}
	out.Merge(m.usage)
	return out
}

// Client calls an OpenAI-compatible chat-completions endpoint with retries.
type Client struct {
	BaseURL string
	// APIKey is read from the environment by the caller; it is only ever
	// sent as the Authorization header.
	APIKey string
	HTTP   *http.Client
	Meter  *Meter
	// MaxAttempts bounds attempts per call (429, 5xx, network errors and
	// empty choices are retried).
	MaxAttempts int
	// BaseDelay is the first backoff; it doubles per attempt with jitter.
	BaseDelay time.Duration
	// MaxDelay caps one backoff.
	MaxDelay time.Duration
	// UserAgent, when set, identifies the client on every request.
	UserAgent string
	// Headers are extra request headers, e.g. a provider's session header.
	Headers map[string]string
	// Codex, when set, sends requests to a ChatGPT plan's Codex backend (BaseURL is
	// then DefaultCodexBaseURL) instead of chat completions.
	Codex *CodexAuth
	// CacheKey, with Codex, is a stable per-question key for prompt caching.
	CacheKey string
	// OnLimit is told when a call waits out the plan's usage limit; may be nil.
	OnLimit func(resetAt time.Time)
	// Sleep waits between attempts; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewClient builds a client with the default retry policy.
func NewClient(baseURL, apiKey string, meter *Meter) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		APIKey:      apiKey,
		HTTP:        &http.Client{Timeout: 15 * time.Minute},
		Meter:       meter,
		MaxAttempts: 8,
		BaseDelay:   2 * time.Second,
		MaxDelay:    2 * time.Minute,
		Sleep:       sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryableError is a failure worth another attempt.
type retryableError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryableError) Error() string { return e.err.Error() }

// Chat sends one request, retrying transient failures, and records usage under role.
func (c *Client) Chat(ctx context.Context, role string, req ChatRequest) (ChatResponse, error) {
	attempts := max(1, c.MaxAttempts)
	var lastErr error
	used := RoleUsage{}
	defer func() {
		if c.Meter != nil {
			c.Meter.record(role, used)
		}
	}()
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			used.Retries++
			delay := c.backoff(attempt)
			var re *retryableError
			if errors.As(lastErr, &re) && re.retryAfter > delay {
				delay = min(re.retryAfter, c.MaxDelay)
			}
			if err := c.Sleep(ctx, delay); err != nil {
				return ChatResponse{}, err
			}
		}
		started := time.Now()
		resp, err := c.once(ctx, req)
		used.LatencyMs += time.Since(started).Milliseconds()
		if err == nil {
			used.Calls++
			used.PromptTokens += resp.Usage.PromptTokens
			used.CompletionTokens += resp.Usage.CompletionTokens
			used.ReasoningTokens += resp.Usage.Reasoning()
			used.CachedTokens += resp.Usage.Cached()
			return resp, nil
		}
		lastErr = err
		if limit := (*UsageLimitError)(nil); errors.As(err, &limit) && ctx.Err() == nil {
			// The plan's window: wait for its reset and send again. The wait spends
			// no attempt, so a run outlasts any number of windows.
			if c.OnLimit != nil {
				c.OnLimit(limit.ResetAt)
			}
			if err := c.Sleep(ctx, time.Until(limit.ResetAt)+30*time.Second); err != nil {
				return ChatResponse{}, err
			}
			attempt--
			continue
		}
		var re *retryableError
		if ctx.Err() != nil || !errors.As(err, &re) {
			break
		}
	}
	used.FailedCalls++
	return ChatResponse{}, lastErr
}

func (c *Client) backoff(attempt int) time.Duration {
	delay := c.BaseDelay << (attempt - 1)
	if delay <= 0 || delay > c.MaxDelay {
		delay = c.MaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(delay)/2 + 1))
	return delay/2 + jitter
}

func (c *Client) once(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if c.Codex != nil {
		return c.codexOnce(ctx, req)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.UserAgent != "" {
		httpReq.Header.Set("User-Agent", c.UserAgent)
	}
	for name, value := range c.Headers {
		httpReq.Header.Set(name, value)
	}
	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return ChatResponse{}, ctx.Err()
		}
		return ChatResponse{}, &retryableError{err: fmt.Errorf("request: %w", err)}
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return ChatResponse{}, &retryableError{err: fmt.Errorf("read body: %w", err)}
	}
	if httpResp.StatusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, snippet(raw))
		if httpResp.StatusCode == http.StatusTooManyRequests || httpResp.StatusCode >= 500 || httpResp.StatusCode == http.StatusRequestTimeout {
			return ChatResponse{}, &retryableError{err: err, retryAfter: parseRetryAfter(httpResp.Header.Get("Retry-After"))}
		}
		return ChatResponse{}, err
	}
	var resp ChatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ChatResponse{}, &retryableError{err: fmt.Errorf("decode response: %w: %s", err, snippet(raw))}
	}
	if len(resp.Choices) == 0 {
		return ChatResponse{}, &retryableError{err: fmt.Errorf("response has no choices: %s", snippet(raw))}
	}
	return resp, nil
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
