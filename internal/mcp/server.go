// Package mcp implements a Model Context Protocol server over stdio.
//
// Exposing AISAI to an agent is the point of this package: an external agent
// (Claude Code, Codex, Cursor) can drive a full package audit — resolve,
// scan, inspect findings, propose and apply a fix — without a cloud account
// and without the agent needing to read the workspace itself.
//
// The implementation speaks JSON-RPC 2.0 over newline-delimited JSON, which
// is the MCP stdio transport.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tlmcguire/aisai/internal/model"
)

// ProtocolVersion is the MCP revision this server implements.
const ProtocolVersion = "2024-11-05"

// ServerName identifies the server to clients.
const ServerName = "aisai"

// ServerVersion is the server version reported to clients.
var ServerVersion = "0.5.0"

// Tool is one callable tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	// Handler runs the tool. Arguments are the raw JSON object from the
	// request.
	Handler func(ctx context.Context, args map[string]any) (string, error) `json:"-"`
}

// Server dispatches MCP requests to registered tools.
type Server struct {
	mu    sync.RWMutex
	tools map[string]Tool

	// maxToolOutput bounds the size of a tool result so a tool cannot blow
	// up the caller's context.
	maxToolOutput int
}

// NewServer creates an empty server.
func NewServer() *Server {
	return &Server{
		tools:         map[string]Tool{},
		maxToolOutput: 64 * 1024,
	}
}

// Register adds a tool.
func (s *Server) Register(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name] = t
}

// Tools returns the registered tools.
func (s *Server) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	return out
}

// jsonrpcRequest is an incoming JSON-RPC message.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is an outgoing JSON-RPC message.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// jsonrpcError codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Serve reads requests from r and writes responses to w until the reader is
// exhausted or the context is cancelled.
//
// Only stdout is used for protocol traffic; anything diagnostic goes to
// stderr, because a stray write to stdout corrupts the stream.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	// Tool results can be large; allow a big line.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	enc := json.NewEncoder(w)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				Error:   &jsonrpcError{Code: codeParseError, Message: "invalid JSON: " + err.Error()},
			})
			continue
		}

		// A request without an id is a notification: process it but do not
		// reply.
		isNotification := len(req.ID) == 0

		result, rerr := s.dispatch(ctx, &req)
		if isNotification {
			continue
		}

		resp := jsonrpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// dispatch handles one method.
func (s *Server) dispatch(ctx context.Context, req *jsonrpcRequest) (interface{}, *jsonrpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    ServerName,
				"version": ServerVersion,
			},
		}, nil

	case "notifications/initialized", "initialized":
		return map[string]any{}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		tools := s.Tools()
		// Keep the order stable so clients see a consistent list.
		sortTools(tools)
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": schema,
			})
		}
		return map[string]any{"tools": list}, nil

	case "tools/call":
		return s.callTool(ctx, req)

	default:
		return nil, &jsonrpcError{Code: codeMethodNotFound, Message: "unknown method: " + req.Method}
	}
}

// callTool runs a tool and wraps the result in MCP content.
func (s *Server) callTool(ctx context.Context, req *jsonrpcRequest) (interface{}, *jsonrpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
		}
	}
	if params.Name == "" {
		return nil, &jsonrpcError{Code: codeInvalidParams, Message: "tool name is required"}
	}

	s.mu.RLock()
	tool, ok := s.tools[params.Name]
	s.mu.RUnlock()
	if !ok {
		return nil, &jsonrpcError{Code: codeMethodNotFound, Message: "unknown tool: " + params.Name}
	}

	output, err := tool.Handler(ctx, params.Arguments)
	if err != nil {
		// A tool failure is reported in-band so the model can react to it,
		// rather than as a protocol error.
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "Error: " + err.Error()}},
			"isError": true,
		}, nil
	}

	if len(output) > s.maxToolOutput {
		dropped := len(output) - s.maxToolOutput
		output = truncateUTF8(output, s.maxToolOutput) +
			fmt.Sprintf("\n\n[output truncated: %d of %d bytes dropped. Ask for the rest explicitly.]", dropped, len(output))
	}

	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": output}},
		"isError": false,
	}, nil
}

// truncateUTF8 cuts a string to at most limit bytes without splitting a
// multi-byte rune.
//
// A byte-boundary cut would leave the agent looking at a replacement
// character at the end of the output, which reads as corrupted content
// rather than as truncation. At most three bytes are given back.
func truncateUTF8(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// sortTools orders tools by name for a stable listing.
func sortTools(tools []Tool) {
	for i := 1; i < len(tools); i++ {
		for j := i; j > 0 && tools[j].Name < tools[j-1].Name; j-- {
			tools[j], tools[j-1] = tools[j-1], tools[j]
		}
	}
}

// StringProp describes a string parameter for a tool schema.
func StringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// IntProp describes an integer parameter for a tool schema.
func IntProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// ObjectSchema builds a tool schema with the given properties and required
// list.
func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// ArgString reads a string argument.
func ArgString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// ArgInt reads an integer argument.
//
// A JSON number arrives as a float64 and a caller may send it as a string, so
// both are accepted. A value that is neither is read as zero, which the
// callers treat as "not set".
func ArgInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// FindingSummary renders a finding as a compact one-line summary for a tool
// result.
func FindingSummary(f model.Finding) string {
	return fmt.Sprintf("[%s] %s:%d %s (%s, confidence %.2f) %s",
		f.Severity, f.File, f.StartLine, f.CWE, f.Tool, f.Confidence, f.Message)
}
