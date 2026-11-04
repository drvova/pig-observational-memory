package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/omtool"
)

// mcpProtocolVersion is the MCP revision the server speaks.
const mcpProtocolVersion = "2025-06-18"

// rpc is one JSON-RPC message.
type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolDescription is the memory tool's MCP description: its fixed description, the line the
// system prompt's tool list carries and its guidelines, the same text for every model.
func toolDescription() string {
	lines := append([]string{omtool.Description, "", omtool.PromptSnippet}, omtool.PromptGuidelines...)
	return strings.Join(lines, "\n")
}

// mcp serves the memory tool over MCP's stdio transport (om mcp --harness <name>), one
// JSON-RPC message per line, for the harness session current in dir (remember): the harness
// starts this server per project and hooks record which session is current there.
func (a *app) mcp(harnessName, dir string, in io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var req rpc
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if err := enc.Encode(rpc{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification (notifications/initialized, cancelled): no answer
		}
		resp := rpc{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "om", "version": Version},
				"instructions":    toolDescription(),
			}
		case "ping":
			resp.Result = map[string]any{}
		case "tools/list":
			resp.Result = map[string]any{"tools": []any{map[string]any{
				"name":        omtool.ToolName,
				"title":       omtool.Label,
				"description": toolDescription(),
				"inputSchema": omtool.Parameters,
			}}}
		case "tools/call":
			resp.Result = a.callTool(harnessName, dir, req.Params)
		default:
			resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// callTool runs one memory tool call on the current session; a failure is a tool error the
// model reads, never a protocol error.
func (a *app) callTool(harnessName, dir string, params json.RawMessage) map[string]any {
	fail := func(msg string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": msg}}, "isError": true}
	}
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return fail("om: bad tools/call params: " + err.Error())
	}
	if p.Name != omtool.ToolName {
		return fail(fmt.Sprintf("om: unknown tool %q", p.Name))
	}
	s, ok := session(a.root, harnessName, dir)
	if !ok {
		return fail("om: no " + harnessName + " session is recorded for " + dir + " yet (the om hooks record it when the session starts or a prompt is sent); run om install " + harnessName)
	}
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return fail(err.Error())
	}
	result := a.manager.RunMemory(h, p.Arguments)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": result.Text}}, "isError": result.IsError}
}

// mcpDir is the directory the server serves: $CLAUDE_PROJECT_DIR when the harness sets it, else
// the server's working directory, where harnesses start their MCP servers.
func mcpDir() string {
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
		return d
	}
	d, _ := os.Getwd()
	return d
}
