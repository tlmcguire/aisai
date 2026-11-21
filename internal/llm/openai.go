package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// openAIMaxAttempts is the total number of HTTP attempts made for a single
// request, including retries.
const openAIMaxAttempts = 3

// openAIHealthTimeout bounds the cheap health probe.
const openAIHealthTimeout = 5 * time.Second

// OpenAIProvider talks to any OpenAI-compatible endpoint.
//
// The API key is only ever sent as a bearer token; it is stripped from every
// error string before the error leaves this package.
type OpenAIProvider struct {
	opts   Options
	base   string
	client *http.Client
}

// NewOpenAICompat returns a provider for an OpenAI-compatible endpoint.
//
// Options.URL is the endpoint root, for example "https://api.openai.com" or
// "http://localhost:8000/v1". A missing "/v1" suffix is added.
func NewOpenAICompat(opts Options) *OpenAIProvider {
	opts.Timeout = opts.timeout()
	opts.NumCtx = opts.numCtx()
	return &OpenAIProvider{
		opts:   opts,
		base:   openAIBase(opts.URL),
		client: &http.Client{Timeout: opts.Timeout},
	}
}

// openAIBase normalises the endpoint root.
func openAIBase(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		u = "https://api.openai.com"
	}
	if strings.HasSuffix(u, "/v1") || strings.Contains(u, "/v1/") {
		return u
	}
	return u + "/v1"
}

// ID returns the stable provider identifier.
func (p *OpenAIProvider) ID() string { return "openai" }

// Kind returns the provider family.
func (p *OpenAIProvider) Kind() string { return "openai" }

// do performs an HTTP request against the OpenAI-compatible endpoint, retrying
// on network errors and 5xx responses with exponential backoff.
func (p *OpenAIProvider) do(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("openai: encode %s request: %w", path, err)
		}
		body = b
	}

	var lastErr error
	for attempt := 1; attempt <= openAIMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, p.base+path, reader)
		if err != nil {
			return nil, fmt.Errorf("openai: build %s request: %w", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if p.opts.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.opts.APIKey)
		}

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("openai: %s %s: %w", method, path, err)
			if !sleepCtx(ctx, backoffDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}
		if resp.StatusCode >= 500 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			lastErr = fmt.Errorf("openai: %s %s: %s: %s", method, path, resp.Status, p.safe(string(msg)))
			if !sleepCtx(ctx, backoffDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("openai: request failed")
	}
	return nil, lastErr
}

// safe removes the API key from a string before it is returned to a caller.
func (p *OpenAIProvider) safe(s string) string {
	return redact(s, p.opts.APIKey)
}

// openAITool is the wire format of a tool definition.
type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// openAIChatRequest is the wire format of POST /v1/chat/completions.
type openAIChatRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	Stream         bool           `json:"stream"`
	Temperature    float64        `json:"temperature,omitempty"`
	Seed           int            `json:"seed,omitempty"`
	Tools          []openAITool   `json:"tools,omitempty"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

// toOpenAITools converts provider tools to the OpenAI wire format.
func toOpenAITools(tools []Tool) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openAITool, 0, len(tools))
	for _, t := range tools {
		var ot openAITool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.Parameters
		out = append(out, ot)
	}
	return out
}

// Chat streams a completion from the OpenAI-compatible endpoint.
//
// The returned channel is closed when the stream ends. A failure that happens
// before the first byte is returned as the error; a failure mid-stream is
// logged and signalled with a terminal chunk carrying Done.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	payload, err := p.chatPayload(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("openai: chat: %s: %s", resp.Status, p.safe(strings.TrimSpace(string(msg))))
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		if err := p.readSSE(ctx, resp, func(delta string, calls []openAIToolCall) bool {
			select {
			case ch <- StreamChunk{Delta: delta}:
				return true
			case <-ctx.Done():
				return false
			}
		}); err != nil {
			log.Printf("openai: chat stream failed: %v", err)
			select {
			case ch <- StreamChunk{Done: true}:
			case <-ctx.Done():
			}
		}
	}()
	return ch, nil
}

// ChatTools streams a completion and reports the tool calls the model asks
// for. It implements the optional ToolProvider interface.
func (p *OpenAIProvider) ChatTools(ctx context.Context, req ChatRequest) (<-chan ToolEvent, error) {
	payload, err := p.chatPayload(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("openai: chat: %s: %s", resp.Status, p.safe(strings.TrimSpace(string(msg))))
	}

	events := make(chan ToolEvent, 64)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		send := func(ev ToolEvent) bool {
			select {
			case events <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		// Tool calls arrive as fragments; they are accumulated per index and
		// emitted once the stream ends.
		calls := map[int]*openAIToolCall{}
		order := []int{}
		err := p.readSSE(ctx, resp, func(delta string, deltas []openAIToolCall) bool {
			for _, c := range deltas {
				cur, ok := calls[c.Index]
				if !ok {
					cur = &openAIToolCall{Index: c.Index}
					calls[c.Index] = cur
					order = append(order, c.Index)
				}
				if c.ID != "" {
					cur.ID = c.ID
				}
				if c.Function.Name != "" {
					cur.Function.Name += c.Function.Name
				}
				cur.Function.Arguments += c.Function.Arguments
			}
			if delta == "" {
				return true
			}
			return send(ToolEvent{Delta: delta})
		})
		for _, idx := range order {
			call := calls[idx]
			args, err := decodeToolArguments(call.Function.Arguments)
			if err != nil {
				log.Printf("openai: tool call %q has invalid arguments: %v", call.Function.Name, err)
				args = map[string]any{}
			}
			if !send(ToolEvent{ToolCall: &ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: args}}) {
				return
			}
		}
		if err != nil {
			log.Printf("openai: tool chat stream failed: %v", err)
			send(ToolEvent{Done: true})
		}
	}()
	return events, nil
}

// openAIToolCall is a (possibly partial) tool call in a streamed response.
type openAIToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// decodeToolArguments parses the JSON arguments of a tool call.
func decodeToolArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// readSSE consumes a server-sent-events stream of chat completions.
func (p *OpenAIProvider) readSSE(ctx context.Context, resp *http.Response, onEvent func(delta string, calls []openAIToolCall) bool) error {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil
		}
		var obj struct {
			Choices []struct {
				Delta struct {
					Content   string           `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			return fmt.Errorf("openai: malformed stream line: %w", err)
		}
		if obj.Error != nil && obj.Error.Message != "" {
			return errors.New(p.safe(obj.Error.Message))
		}
		for _, c := range obj.Choices {
			if !onEvent(c.Delta.Content, c.Delta.ToolCalls) {
				return ctx.Err()
			}
		}
	}
	return sc.Err()
}

// chatPayload builds the /v1/chat/completions request body.
func (p *OpenAIProvider) chatPayload(req ChatRequest, stream bool) (*openAIChatRequest, error) {
	model := req.Model
	if model == "" {
		model = p.opts.chatModel()
	}
	if model == "" {
		return nil, errors.New("openai: no model configured")
	}
	payload := &openAIChatRequest{
		Model:       model,
		Messages:    req.Messages,
		Stream:      stream,
		Temperature: req.Temperature,
		Tools:       toOpenAITools(req.Tools),
	}
	if req.Seed != 0 {
		payload.Seed = req.Seed
	}
	if req.Schema != nil {
		payload.ResponseFormat = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "result",
				"schema": req.Schema,
			},
		}
	}
	return payload, nil
}

// Complete performs a single completion and parses a structured reply.
func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (*Completion, error) {
	var msgs []Message
	if req.System != "" {
		msgs = append(msgs, Message{Role: RoleSystem, Content: req.System})
	}
	msgs = append(msgs, Message{Role: RoleUser, Content: req.User})

	payload, err := p.chatPayload(ChatRequest{
		Model:       req.Model,
		Messages:    msgs,
		Schema:      req.Schema,
		NumCtx:      req.NumCtx,
		Temperature: req.Temperature,
		Seed:        req.Seed,
		Think:       req.Think,
	}, false)
	if err != nil {
		return nil, err
	}

	resp, err := p.do(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai: chat: %s: %s", resp.Status, p.safe(strings.TrimSpace(string(msg))))
	}

	var obj struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("openai: decode chat response: %w", err)
	}
	if obj.Error != nil && obj.Error.Message != "" {
		return nil, fmt.Errorf("openai: chat: %s", p.safe(obj.Error.Message))
	}
	if len(obj.Choices) == 0 {
		return nil, errors.New("openai: chat: response contained no choices")
	}
	return parseCompletion(obj.Choices[0].Message.Content)
}

// openAIEmbedResponse is the wire format of POST /v1/embeddings.
type openAIEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed returns one embedding vector per input text, in input order.
func (p *OpenAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	model := p.opts.embedModel()
	if model == "" {
		return nil, errors.New("openai: no embedding model configured")
	}
	payload := map[string]any{"model": model, "input": texts}
	resp, err := p.do(ctx, http.MethodPost, "/embeddings", payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai: embed: %s: %s", resp.Status, p.safe(strings.TrimSpace(string(msg))))
	}
	var obj openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("openai: decode embed response: %w", err)
	}
	if obj.Error != nil && obj.Error.Message != "" {
		return nil, fmt.Errorf("openai: embed: %s", p.safe(obj.Error.Message))
	}
	out := make([][]float32, len(texts))
	for _, d := range obj.Data {
		if d.Index < 0 || d.Index >= len(out) {
			continue
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("openai: embed: missing embedding for input %d", i)
		}
	}
	return out, nil
}

// Models lists the models available on the endpoint.
func (p *OpenAIProvider) Models(ctx context.Context) ([]string, error) {
	resp, err := p.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai: models: %s: %s", resp.Status, p.safe(strings.TrimSpace(string(msg))))
	}
	var obj struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("openai: decode models response: %w", err)
	}
	names := make([]string, 0, len(obj.Data))
	for _, m := range obj.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

// Health probes the endpoint with a cheap GET /v1/models.
func (p *OpenAIProvider) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, openAIHealthTimeout)
	defer cancel()
	resp, err := p.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("openai: health: %s: %s", resp.Status, p.safe(strings.TrimSpace(string(msg))))
	}
	return nil
}
