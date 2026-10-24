package bench

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeJWT is a token whose only content is its exp claim.
func fakeJWT(exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	return "h." + payload + ".s"
}

func writeAuth(t *testing.T, dir, access, refresh, issuer string) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	data, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{"access_token": access, "refresh_token": refresh, "id_token": "id", "account_id": "acct",
			"oauth_provenance": map[string]any{"issuer": issuer, "client_id": "client"}},
	})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

func TestToResponsesTranslatesRolesAndTools(t *testing.T) {
	req := ChatRequest{Model: "m", ReasoningEffort: "low", MaxTokens: 10, Messages: []ChatMessage{
		{Role: "system", Content: Text("sys")},
		{Role: "user", Content: Text("hi")},
		{Role: "assistant", Content: Text("calling"), ToolCalls: []ToolCall{{ID: "c1", Type: "function", Function: FunctionCall{Name: "record", Arguments: `{"a":1}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: Text("done")},
	}, Tools: []ToolSpec{{Type: "function", Function: FunctionSpec{Name: "record", Parameters: map[string]any{"type": "object"}}}}}
	out := toResponses(req, "key")
	if out.Instructions != "sys" || out.Store || !out.Stream || out.PromptCacheKey != "key" || out.ToolChoice != "auto" || out.Reasoning["effort"] != "low" {
		t.Fatalf("request fields: %+v", out)
	}
	kinds := []string{}
	for _, item := range out.Input {
		kinds = append(kinds, fmt.Sprint(item["type"], "/", item["role"]))
	}
	if got := strings.Join(kinds, ","); got != "message/user,message/assistant,function_call/<nil>,function_call_output/<nil>" {
		t.Fatalf("input items: %s", got)
	}
	if len(out.Tools) != 1 || out.Tools[0]["name"] != "record" {
		t.Fatalf("tools: %+v", out.Tools)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "max_tokens") || strings.Contains(string(raw), "temperature") {
		t.Fatalf("sends a field the endpoint rejects: %s", raw)
	}
	if toResponses(ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: Text("q")}}}, "").Instructions == "" {
		t.Fatal("instructions must never be empty")
	}
}

func TestCodexClientRoundTripRefreshesOn401(t *testing.T) {
	dir := t.TempDir()
	var calls, refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshes.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": fakeJWT(time.Now().Add(time.Hour)), "refresh_token": "r2"})
		case "/codex/responses":
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("chatgpt-account-id") != "acct" || r.Header.Get("session_id") != "longmemeval-q1" {
				t.Errorf("headers: %v", r.Header)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"prompt_cache_key":"longmemeval-q1"`) {
				t.Errorf("body: %s", body)
			}
			fmt.Fprint(w, sse(`{"type":"response.created"}`,
				`{"type":"response.completed","response":{"model":"m","status":"completed","output":[{"type":"reasoning","summary":[{"text":"think"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","call_id":"c9","name":"record","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14,"input_tokens_details":{"cached_tokens":6},"output_tokens_details":{"reasoning_tokens":2}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	path := writeAuth(t, dir, fakeJWT(time.Now().Add(time.Hour)), "r1", srv.URL)
	auth, err := NewCodexAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	meter := NewMeter(nil)
	client := NewClient(srv.URL+"/codex", "", meter)
	client.Codex, client.CacheKey = auth, "longmemeval-q1"
	resp, err := client.Chat(context.Background(), RoleObserver, ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: Text("hi")}}})
	if err != nil {
		t.Fatal(err)
	}
	msg := resp.Choices[0].Message
	if msg.Text() != "ok" || msg.ReasoningText() != "think" || len(msg.ToolCalls) != 1 || resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("response: %+v", resp)
	}
	if u := meter.Snapshot()[RoleObserver]; u.PromptTokens != 10 || u.CachedTokens != 6 || u.ReasoningTokens != 2 || u.Calls != 1 {
		t.Fatalf("usage: %+v", u)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want 1 (after the 401)", refreshes.Load())
	}
	var saved map[string]map[string]any
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &saved)
	if saved["tokens"]["refresh_token"] != "r2" {
		t.Fatalf("the rotated refresh token was not saved: %v", saved["tokens"]["refresh_token"])
	}
}

func TestCodexRefreshAdoptsAnotherWorkersToken(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	path := writeAuth(t, dir, fakeJWT(time.Now().Add(time.Minute)), "r1", srv.URL) // inside the refresh margin
	auth, err := NewCodexAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh := fakeJWT(time.Now().Add(time.Hour))
	writeAuth(t, dir, fresh, "r2", srv.URL) // another worker refreshed meanwhile
	access, _, err := auth.Token(context.Background(), false)
	if err != nil || access != fresh {
		t.Fatalf("token = %q, %v; want the other worker's fresh token without a refresh", access, err)
	}
}

func TestCodexUsageLimitWaitsForTheReset(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"type":"usage_limit_reached","message":"limit","resets_in_seconds":3600}}`)
			return
		}
		fmt.Fprint(w, sse(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`))
	}))
	defer srv.Close()
	auth, err := NewCodexAuth(writeAuth(t, dir, fakeJWT(time.Now().Add(time.Hour)), "r1", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(srv.URL, "", NewMeter(nil))
	client.Codex, client.MaxAttempts = auth, 1 // waits spend no attempt
	var slept []time.Duration
	client.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	limits := 0
	client.OnLimit = func(time.Time) { limits++ }
	resp, err := client.Chat(context.Background(), RoleActor, ChatRequest{Model: "m"})
	if err != nil || resp.Choices[0].Message.Text() != "ok" {
		t.Fatalf("resp %+v, err %v", resp, err)
	}
	if limits != 2 || len(slept) != 2 || slept[0] < 59*time.Minute {
		t.Fatalf("limits %d, slept %v; want two waits of about an hour", limits, slept)
	}
}

func TestCodexOrdinary429IsRetried(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	var re *retryableError
	if err := codexHTTPError(resp, []byte(`{"error":{"message":"slow down"}}`), time.Now()); !errorsAs(err, &re) {
		t.Fatalf("a plain 429 should be retryable, got %T", err)
	}
}

func errorsAs(err error, target **retryableError) bool {
	r, ok := err.(*retryableError)
	if ok {
		*target = r
	}
	return ok
}

func TestChatUsageLimitWaitsWithoutSpendingAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"type":"error","error":{"type":"PlanUsageLimitError","message":"usage limit exceeded"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "k", NewMeter(nil))
	client.MaxAttempts = 1 // waits spend no attempt
	var slept []time.Duration
	client.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	resp, err := client.Chat(context.Background(), RoleActor, ChatRequest{Model: "m"})
	if err != nil || resp.Choices[0].Message.Text() != "ok" {
		t.Fatalf("resp %+v, err %v", resp, err)
	}
	if len(slept) != 2 || slept[0] < ChatUsageLimitRecheck-time.Minute {
		t.Fatalf("slept %v; want two waits of about %v", slept, ChatUsageLimitRecheck)
	}
}

func TestChatRateLimitIsNotAUsageLimit(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	if limit := chatUsageLimit(resp, []byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`), time.Now()); limit != nil {
		t.Fatalf("a rate limit should be retried with backoff, got %+v", limit)
	}
}
