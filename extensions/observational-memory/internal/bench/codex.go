package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The Codex backend: a ChatGPT plan's Responses endpoint, signed in through the
// Codex CLI (`codex login`). Requests are translated from chat completions to the
// Responses format and back, so every role runs unchanged.

// DefaultCodexBaseURL is the Codex backend's base; requests go to /responses.
const DefaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"

// CodexAuth reads the Codex CLI's auth.json and keeps its access token fresh.
// Tokens never leave this type except as request headers. Workers are separate
// processes sharing one file, so a refresh holds an exclusive lock on a sibling
// lock file and re-reads the file first: refresh tokens rotate, and a second
// refresh with a spent one would sign every worker out.
type CodexAuth struct {
	Path string
	HTTP *http.Client
	// Now is the clock; tests replace it.
	Now func() time.Time

	mu      sync.Mutex
	access  string
	account string
	expires time.Time
}

// DefaultCodexAuthPath is $CODEX_HOME/auth.json, else ~/.codex/auth.json.
func DefaultCodexAuthPath() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "auth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "auth.json")
}

// NewCodexAuth loads the token at path.
func NewCodexAuth(path string) (*CodexAuth, error) {
	a := &CodexAuth{Path: path, HTTP: &http.Client{Timeout: time.Minute}, Now: time.Now}
	f, err := a.read()
	if err != nil {
		return nil, err
	}
	a.adopt(f)
	return a, nil
}

type codexAuthFile struct {
	raw    map[string]any
	tokens map[string]any
}

func (f codexAuthFile) str(key string) string {
	s, _ := f.tokens[key].(string)
	return s
}

func (a *CodexAuth) read() (codexAuthFile, error) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return codexAuthFile{}, fmt.Errorf("codex auth: %w (sign in with `codex login`)", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return codexAuthFile{}, fmt.Errorf("codex auth: %s: %w", a.Path, err)
	}
	tokens, _ := raw["tokens"].(map[string]any)
	f := codexAuthFile{raw: raw, tokens: tokens}
	if tokens == nil || f.str("access_token") == "" || f.str("account_id") == "" {
		return codexAuthFile{}, fmt.Errorf("codex auth: %s has no ChatGPT sign-in (run `codex login`)", a.Path)
	}
	return f, nil
}

func (a *CodexAuth) adopt(f codexAuthFile) {
	a.access, a.account = f.str("access_token"), f.str("account_id")
	a.expires = jwtExpiry(a.access)
}

// jwtExpiry is a token's exp claim, unverified (the server checks it), or zero.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(int64(claims.Exp), 0)
}

// refreshMargin is how long before expiry a token is renewed.
const refreshMargin = 5 * time.Minute

// Token is the current access token and account id, renewed when it is close to
// expiry or when force is set (after a 401).
func (a *CodexAuth) Token(ctx context.Context, force bool) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !force && (a.expires.IsZero() || a.Now().Add(refreshMargin).Before(a.expires)) {
		return a.access, a.account, nil
	}
	if err := a.refresh(ctx, a.access); err != nil {
		return "", "", err
	}
	return a.access, a.account, nil
}

func (a *CodexAuth) refresh(ctx context.Context, stale string) error {
	lock, err := os.OpenFile(a.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("codex auth: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("codex auth: lock: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	// Another worker (or the CLI) may have refreshed while this one waited.
	f, err := a.read()
	if err != nil {
		return err
	}
	if current := f.str("access_token"); current != stale {
		if exp := jwtExpiry(current); exp.IsZero() || a.Now().Add(refreshMargin).Before(exp) {
			a.adopt(f)
			return nil
		}
	}
	refreshToken := f.str("refresh_token")
	if refreshToken == "" {
		return errors.New("codex auth: no refresh token (run `codex login` again)")
	}
	issuer, clientID := "https://auth.openai.com", ""
	if p, ok := f.tokens["oauth_provenance"].(map[string]any); ok {
		if s, _ := p["issuer"].(string); s != "" {
			issuer = strings.TrimRight(s, "/")
		}
		clientID, _ = p["client_id"].(string)
	}
	if clientID == "" {
		return errors.New("codex auth: auth.json names no OAuth client (run `codex login` again)")
	}
	body, _ := json.Marshal(map[string]string{"client_id": clientID, "grant_type": "refresh_token", "refresh_token": refreshToken, "scope": "openid profile email"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("codex auth: refresh: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// The body can echo request fields; report the status only.
		return fmt.Errorf("codex auth: refresh failed with HTTP %d (run `codex login` again)", resp.StatusCode)
	}
	var fresh struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &fresh); err != nil || fresh.AccessToken == "" {
		return errors.New("codex auth: refresh returned no access token")
	}
	f.tokens["access_token"] = fresh.AccessToken
	if fresh.IDToken != "" {
		f.tokens["id_token"] = fresh.IDToken
	}
	if fresh.RefreshToken != "" {
		f.tokens["refresh_token"] = fresh.RefreshToken
	}
	f.raw["last_refresh"] = a.Now().UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(f.raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.Path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("codex auth: %w", err)
	}
	if err := os.Rename(tmp, a.Path); err != nil {
		return fmt.Errorf("codex auth: %w", err)
	}
	a.adopt(f)
	return nil
}

// UsageLimitError is the plan's usage limit: the window resets at ResetAt.
type UsageLimitError struct {
	ResetAt time.Time
	Message string
}

func (e *UsageLimitError) Error() string {
	return fmt.Sprintf("usage limit reached until %s: %s", e.ResetAt.Format(time.RFC3339), e.Message)
}

// --- Translation -------------------------------------------------------------------

type responsesRequest struct {
	Model             string           `json:"model"`
	Instructions      string           `json:"instructions"`
	Input             []map[string]any `json:"input"`
	Tools             []map[string]any `json:"tools,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	ParallelToolCalls bool             `json:"parallel_tool_calls"`
	Reasoning         map[string]any   `json:"reasoning,omitempty"`
	Store             bool             `json:"store"`
	Stream            bool             `json:"stream"`
	PromptCacheKey    string           `json:"prompt_cache_key,omitempty"`
}

// defaultInstructions stands in when a request has no system message: the
// endpoint requires instructions.
const defaultInstructions = "You are a helpful assistant."

// toResponses translates a chat-completions request. System messages become the
// instructions; the endpoint sets its own output limit and sampling, so max_tokens
// and temperature are not sent.
func toResponses(req ChatRequest, cacheKey string) responsesRequest {
	out := responsesRequest{Model: req.Model, Store: false, Stream: true, PromptCacheKey: cacheKey, Input: []map[string]any{}}
	var instructions []string
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			instructions = append(instructions, m.Text())
		case "user":
			out.Input = append(out.Input, map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": m.Text()}}})
		case "assistant":
			if text := m.Text(); text != "" {
				out.Input = append(out.Input, map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": text}}})
			}
			for _, call := range m.ToolCalls {
				out.Input = append(out.Input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
		case "tool":
			out.Input = append(out.Input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Text()})
		}
	}
	out.Instructions = strings.Join(instructions, "\n\n")
	if strings.TrimSpace(out.Instructions) == "" {
		out.Instructions = defaultInstructions
	}
	for _, t := range req.Tools {
		params := t.Function.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out.Tools = append(out.Tools, map[string]any{"type": "function", "name": t.Function.Name, "description": t.Function.Description, "parameters": params, "strict": false})
	}
	if len(out.Tools) > 0 {
		out.ToolChoice = "auto"
	}
	if req.ReasoningEffort != "" {
		out.Reasoning = map[string]any{"effort": req.ReasoningEffort}
	}
	return out
}

type responsesResult struct {
	Model             string `json:"model"`
	Status            string `json:"status"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Text string `json:"text"`
		} `json:"summary"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"output"`
	Usage struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		TotalTokens        int `json:"total_tokens"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

// fromResponses translates a finished response into one chat-completions choice.
func fromResponses(r responsesResult) ChatResponse {
	msg := ChatMessage{Role: "assistant"}
	var text, reasoning []string
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text = append(text, c.Text)
				}
			}
		case "reasoning":
			for _, s := range item.Summary {
				reasoning = append(reasoning, s.Text)
			}
		case "function_call":
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: item.CallID, Type: "function", Function: FunctionCall{Name: item.Name, Arguments: item.Arguments}})
		}
	}
	if len(text) > 0 {
		msg.Content = Text(strings.Join(text, ""))
	}
	msg.ReasoningContent = strings.Join(reasoning, "\n")
	finish := "stop"
	switch {
	case len(msg.ToolCalls) > 0:
		finish = "tool_calls"
	case r.Status == "incomplete" && r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens":
		finish = "length"
	}
	usage := ChatUsage{PromptTokens: r.Usage.InputTokens, CompletionTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.TotalTokens, ReasoningTokens: r.Usage.OutputTokensDetails.ReasoningTokens}
	usage.PromptTokensDetails = &struct {
		CachedTokens int `json:"cached_tokens"`
	}{CachedTokens: r.Usage.InputTokensDetails.CachedTokens}
	return ChatResponse{Model: r.Model, Choices: []ChatChoice{{Message: msg, FinishReason: finish}}, Usage: usage}
}

// --- Transport -----------------------------------------------------------------------

func (c *Client) codexOnce(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := json.Marshal(toResponses(req, c.CacheKey))
	if err != nil {
		return ChatResponse{}, err
	}
	for attempt := 0; ; attempt++ {
		access, account, err := c.Codex.Token(ctx, attempt > 0)
		if err != nil {
			return ChatResponse{}, err
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return ChatResponse{}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Authorization", "Bearer "+access)
		httpReq.Header.Set("chatgpt-account-id", account)
		httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
		httpReq.Header.Set("originator", "codex_cli_rs")
		if c.CacheKey != "" {
			httpReq.Header.Set("session_id", c.CacheKey)
		}
		if c.UserAgent != "" {
			httpReq.Header.Set("User-Agent", c.UserAgent)
		}
		for name, value := range c.Headers {
			httpReq.Header.Set(name, value)
		}
		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				return ChatResponse{}, ctx.Err()
			}
			return ChatResponse{}, &retryableError{err: fmt.Errorf("request: %w", err)}
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			continue // renew the token once and resend
		}
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return ChatResponse{}, codexHTTPError(resp, raw, c.now())
		}
		result, err := readResponsesStream(resp.Body)
		resp.Body.Close()
		if err != nil {
			return ChatResponse{}, err
		}
		chat := fromResponses(result)
		if result.Status == "failed" {
			msg := "response failed"
			if result.Error != nil {
				msg = result.Error.Code + ": " + result.Error.Message
			}
			return ChatResponse{}, &retryableError{err: errors.New(msg)}
		}
		return chat, nil
	}
}

func (c *Client) now() time.Time {
	if c.Codex != nil && c.Codex.Now != nil {
		return c.Codex.Now()
	}
	return time.Now()
}

// codexHTTPError classifies a failed status. A 429 whose error is the plan's usage
// limit carries the reset time; other 429s and 5xx are retried as usual.
func codexHTTPError(resp *http.Response, raw []byte, now time.Time) error {
	err := fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(raw))
	if resp.StatusCode == http.StatusTooManyRequests {
		var body struct {
			Error struct {
				Type            string  `json:"type"`
				Code            string  `json:"code"`
				Message         string  `json:"message"`
				ResetsAt        float64 `json:"resets_at"`
				ResetsInSeconds float64 `json:"resets_in_seconds"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &body) == nil && (body.Error.Type == "usage_limit_reached" || body.Error.Code == "usage_limit_reached" || body.Error.ResetsAt > 0 || body.Error.ResetsInSeconds > 0) {
			reset := now.Add(time.Hour) // when the reply names no time, look again in an hour
			switch {
			case body.Error.ResetsAt > 0:
				reset = time.Unix(int64(body.Error.ResetsAt), 0)
			case body.Error.ResetsInSeconds > 0:
				reset = now.Add(time.Duration(body.Error.ResetsInSeconds * float64(time.Second)))
			}
			return &UsageLimitError{ResetAt: reset, Message: body.Error.Message}
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
		return &retryableError{err: err, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return err
}

// readResponsesStream reads server-sent events to the final response.
func readResponsesStream(r io.Reader) (responsesResult, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var final *responsesResult
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string           `json:"type"`
			Response *responsesResult `json:"response"`
			Message  string           `json:"message"`
			Code     string           `json:"code"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "response.completed", "response.incomplete", "response.failed":
			final = event.Response
		case "error":
			return responsesResult{}, &retryableError{err: fmt.Errorf("stream error %s: %s", event.Code, event.Message)}
		}
	}
	if err := scanner.Err(); err != nil {
		return responsesResult{}, &retryableError{err: fmt.Errorf("read stream: %w", err)}
	}
	if final == nil {
		return responsesResult{}, &retryableError{err: errors.New("stream ended without a final response")}
	}
	return *final, nil
}
