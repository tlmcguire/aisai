package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// serve feeds the given JSON-RPC lines to the server and returns the decoded
// response objects, one per output line.
func serve(t *testing.T, srv *Server, input string) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(input), &buf); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("response line is not JSON: %v (%q)", err, line)
		}
		out = append(out, msg)
	}
	return out
}

// result returns the "result" object of a response.
func result(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	res, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", msg)
	}
	return res
}

// toolResultText returns the text of the first content block of a tool call
// result.
func toolResultText(t *testing.T, msg map[string]any) string {
	t.Helper()
	res := result(t, msg)
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("tool result has no content: %v", res)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("tool content is not an object: %v", content[0])
	}
	text, _ := block["text"].(string)
	return text
}

func TestInitializeReportsProtocolAndCapabilities(t *testing.T) {
	srv := NewServer()
	out := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(out) != 1 {
		t.Fatalf("expected exactly one response, got %d", len(out))
	}
	res := result(t, out[0])

	if got := res["protocolVersion"]; got != ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", got, ProtocolVersion)
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("no capabilities: %v", res)
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("no tools capability: %v", caps)
	}
	info, ok := res["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("no serverInfo: %v", res)
	}
	if info["name"] != ServerName {
		t.Fatalf("serverInfo.name = %v, want %q", info["name"], ServerName)
	}
}

func TestToolsListIsSortedAndStable(t *testing.T) {
	srv := NewServer()
	for _, name := range []string{"zeta_tool", "alpha_tool", "middle_tool"} {
		srv.Register(Tool{
			Name:        name,
			Description: "test tool",
			InputSchema: ObjectSchema(nil),
			Handler:     func(context.Context, map[string]any) (string, error) { return "ok", nil },
		})
	}

	out := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(out) != 1 {
		t.Fatalf("expected one response, got %d", len(out))
	}
	tools, ok := result(t, out[0])["tools"].([]any)
	if !ok {
		t.Fatalf("no tools array")
	}
	var names []string
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tool is not an object: %v", item)
		}
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	want := []string{"alpha_tool", "middle_tool", "zeta_tool"}
	if len(names) != len(want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tool names = %v, want %v", names, want)
		}
	}

	// The listing must be identical on a second call.
	again := serve(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	first, _ := json.Marshal(result(t, out[0])["tools"])
	second, _ := json.Marshal(result(t, again[0])["tools"])
	if !bytes.Equal(first, second) {
		t.Fatalf("tools/list is not stable:\n%s\n%s", first, second)
	}
}

func TestUnknownToolIsMethodNotFound(t *testing.T) {
	srv := NewServer()
	out := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`)
	if len(out) != 1 {
		t.Fatalf("expected one response, got %d", len(out))
	}
	if _, ok := out[0]["result"]; ok {
		t.Fatalf("unknown tool must not produce a result: %v", out[0])
	}
	errObj, ok := out[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown tool must produce an error: %v", out[0])
	}
	if code, _ := errObj["code"].(float64); int(code) != codeMethodNotFound {
		t.Fatalf("error code = %v, want %d", errObj["code"], codeMethodNotFound)
	}
}

func TestToolErrorIsReportedInBand(t *testing.T) {
	srv := NewServer()
	srv.Register(Tool{
		Name:        "explodes",
		Description: "a tool that always fails",
		InputSchema: ObjectSchema(nil),
		Handler: func(context.Context, map[string]any) (string, error) {
			return "", errNoAudit
		},
	})

	out := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"explodes","arguments":{}}}`)
	if len(out) != 1 {
		t.Fatalf("expected one response, got %d", len(out))
	}
	if _, ok := out[0]["error"]; ok {
		t.Fatalf("a failing tool must not produce a JSON-RPC error: %v", out[0])
	}
	res := result(t, out[0])
	if isError, _ := res["isError"].(bool); !isError {
		t.Fatalf("isError = %v, want true: %v", res["isError"], res)
	}
	if text := toolResultText(t, out[0]); !strings.Contains(text, errNoAudit.Error()) {
		t.Fatalf("tool text = %q, want it to mention %q", text, errNoAudit.Error())
	}
}

func TestLargeToolOutputIsTruncated(t *testing.T) {
	srv := NewServer()
	srv.Register(Tool{
		Name:        "huge",
		Description: "a tool that returns a lot of text",
		InputSchema: ObjectSchema(nil),
		Handler: func(context.Context, map[string]any) (string, error) {
			return strings.Repeat("x", 200*1024), nil
		},
	})

	out := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"huge","arguments":{}}}`)
	text := toolResultText(t, out[0])
	if !strings.Contains(text, "[output truncated") {
		t.Fatal("the truncated output must carry the truncation marker")
	}
	// The marker has to say how much was dropped, so that the agent knows
	// it is looking at a part of the result.
	if !strings.Contains(text, "bytes dropped") {
		t.Fatalf("the truncation marker does not say how much was dropped: %q", text[len(text)-120:])
	}
	if len(text) > 64*1024+128 {
		t.Fatalf("truncated output is %d bytes, want about %d", len(text), 64*1024)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	srv := NewServer()
	out := serve(t, srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
		`{"jsonrpc":"2.0","method":"ping"}`)
	if len(out) != 0 {
		t.Fatalf("a notification must not produce a response, got %v", out)
	}
}

func TestUnknownMethodIsRejected(t *testing.T) {
	srv := NewServer()
	out := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"no/such/method"}`)
	if len(out) != 1 {
		t.Fatalf("expected one response, got %d", len(out))
	}
	errObj, ok := out[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error: %v", out[0])
	}
	if code, _ := errObj["code"].(float64); int(code) != codeMethodNotFound {
		t.Fatalf("error code = %v, want %d", errObj["code"], codeMethodNotFound)
	}
}

// TestTruncateUTF8DoesNotSplitARune pins the boundary of the output cap.
//
// The cap used to cut the output at a byte boundary, so a multi-byte rune
// could be split and the agent would see a replacement character at the end
// of the output. The cut now backs off to the nearest rune boundary.
func TestTruncateUTF8DoesNotSplitARune(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		limit int
	}{
		{name: "ascii", text: "hello world", limit: 5},
		{name: "rune on the boundary", text: "héllo wörld", limit: 2},
		{name: "rune split by one byte", text: "日本語のテキスト", limit: 4},
		{name: "emoji", text: "done 🎉", limit: 5},
		{name: "limit zero", text: "hello", limit: 0},
		{name: "under the limit", text: "hi", limit: 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateUTF8(tc.text, tc.limit)
			if len(got) > tc.limit && tc.limit > 0 {
				t.Errorf("the result is %d bytes, want at most %d", len(got), tc.limit)
			}
			if !utf8.ValidString(got) {
				t.Errorf("the result is not valid UTF-8: %q", got)
			}
			if !strings.HasPrefix(tc.text, got) {
				t.Errorf("the result is not a prefix of the input: %q", got)
			}
		})
	}
}
