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
	"sync"
	"time"
)

// Ollama provider endpoints.
const (
	ollamaChatPath     = "/api/chat"
	ollamaEmbedPath    = "/api/embed"
	ollamaTagsPath     = "/api/tags"
	ollamaShowPath     = "/api/show"
	ollamaGeneratePath = "/api/generate"
)

// ollamaMaxAttempts is the total number of HTTP attempts made for a single
// request, including retries and host failover.
const ollamaMaxAttempts = 3

// ollamaHealthTimeout bounds the cheap health probe.
const ollamaHealthTimeout = 3 * time.Second

// OllamaProvider talks to a local Ollama server.
//
// The provider keeps a list of candidate base URLs: when one stops answering
// the next candidate is used, which reproduces the multi-host failover of the
// legacy application (host.containers.internal, 127.0.0.1, 0.0.0.0).
type OllamaProvider struct {
	opts   Options
	urls   []string
	client *http.Client

	// models holds the models in use. When it is nil the provider falls
	// back to the model names baked into opts.
	models *Models

	mu     sync.Mutex
	active int
}

// NewOllama returns a provider for the given options.
//
// Options.URL may hold a comma-separated list of candidate base URLs; they are
// tried in order until one answers.
func NewOllama(opts Options) *OllamaProvider {
	opts.Timeout = opts.timeout()
	opts.NumCtx = opts.numCtx()
	urls := splitURLs(opts.URL)
	if len(urls) == 0 {
		urls = []string{"http://127.0.0.1:11434"}
	}
	return &OllamaProvider{
		opts:   opts,
		urls:   urls,
		client: &http.Client{Timeout: opts.Timeout},
		models: opts.Models,
	}
}

// chatModel returns the model used for chat turns.
func (p *OllamaProvider) chatModel() string {
	if p.models != nil {
		if name := p.models.Chat(); name != "" {
			return name
		}
	}
	return p.opts.chatModel()
}

// embedModel returns the model used for embeddings.
func (p *OllamaProvider) embedModel() string {
	if p.models != nil {
		if name := p.models.Embed(); name != "" {
			return name
		}
	}
	return p.opts.embedModel()
}

// numCtx returns the effective context window.
func (p *OllamaProvider) numCtx() int {
	if p.models != nil {
		return p.models.NumCtx()
	}
	return p.opts.numCtx()
}

// keepAlive returns the keep-alive window sent with every request.
//
// Ollama unloads a model once no request has used it for this long, which
// is what returns the memory to the rest of the system on a small laptop.
func (p *OllamaProvider) keepAlive() string {
	if p.models != nil {
		if s := p.models.KeepAlive(); s != "" {
			return s
		}
	}
	if p.opts.KeepAlive != "" {
		return p.opts.KeepAlive
	}
	return DefaultKeepAlive
}

// ID returns the stable provider identifier.
func (p *OllamaProvider) ID() string { return "ollama" }

// Kind returns the provider family.
func (p *OllamaProvider) Kind() string { return "ollama" }

// baseFor returns the base URL to use for the given attempt, together with
// its index in the candidate list.
func (p *OllamaProvider) baseFor(attempt int) (string, int) {
	p.mu.Lock()
	start := p.active
	p.mu.Unlock()
	idx := (start + attempt - 1) % len(p.urls)
	return p.urls[idx], idx
}

// setActive records the candidate that answered last.
func (p *OllamaProvider) setActive(idx int) {
	p.mu.Lock()
	p.active = idx
	p.mu.Unlock()
}

// do performs an HTTP request against the Ollama API, retrying on network
// errors and 5xx responses with exponential backoff, and failing over to the
// next candidate host when one stops answering.
func (p *OllamaProvider) do(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("ollama: encode %s request: %w", path, err)
		}
		body = b
	}

	var lastErr error
	for attempt := 1; attempt <= ollamaMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		base, idx := p.baseFor(attempt)
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
		if err != nil {
			return nil, fmt.Errorf("ollama: build %s request: %w", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ollama: %s %s: %w", method, path, err)
			if !sleepCtx(ctx, backoffDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}
		if resp.StatusCode >= 500 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			lastErr = fmt.Errorf("ollama: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
			if !sleepCtx(ctx, backoffDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}
		p.setActive(idx)
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("ollama: request failed")
	}
	return nil, lastErr
}

// ollamaChatRequest is the wire format of POST /api/chat.
type ollamaChatRequest struct {
	Model     string          `json:"model"`
	Messages  []Message       `json:"messages"`
	Stream    bool            `json:"stream"`
	Format    json.RawMessage `json:"format,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Think     *bool           `json:"think,omitempty"`
	Tools     []Tool          `json:"tools,omitempty"`
}

// ollamaToolCall is a tool invocation reported by the model.
type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

// ollamaStreamLine is one NDJSON object of a streamed /api/chat response.
type ollamaStreamLine struct {
	Message struct {
		Content string           `json:"content"`
		Think   string           `json:"thinking"`
		Calls   []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

// chatPayload builds the /api/chat request body.
func (p *OllamaProvider) chatPayload(req ChatRequest, stream bool) (*ollamaChatRequest, error) {
	model := req.Model
	if model == "" {
		model = p.chatModel()
	}
	if model == "" {
		return nil, errors.New("ollama: no model configured")
	}
	numCtx := req.NumCtx
	if numCtx <= 0 {
		numCtx = p.numCtx()
	}
	temperature := req.Temperature
	seed := req.Seed
	if seed == 0 {
		seed = DefaultSeed
	}

	body := &ollamaChatRequest{
		Model:    model,
		Messages: req.Messages,
		Stream:   stream,
		Options: map[string]any{
			"num_ctx":     numCtx,
			"temperature": temperature,
			"seed":        seed,
		},
		KeepAlive: p.keepAlive(),
	}
	if req.Schema != nil {
		raw, err := json.Marshal(req.Schema)
		if err != nil {
			return nil, fmt.Errorf("ollama: encode response schema: %w", err)
		}
		body.Format = raw
	}
	if req.Think {
		think := true
		body.Think = &think
	}
	if len(req.Tools) > 0 {
		body.Tools = req.Tools
	}
	return body, nil
}

// Chat streams a completion from the Ollama server.
//
// The returned channel is closed when the stream ends. A failure that happens
// before the first byte is returned as the error; a failure mid-stream is
// logged and signalled with a terminal chunk carrying Done.
func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	payload, err := p.chatPayload(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, http.MethodPost, ollamaChatPath, payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("ollama: chat: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		err := p.readChatStream(ctx, resp,
			func(delta string) bool {
				select {
				case ch <- StreamChunk{Delta: delta}:
					return true
				case <-ctx.Done():
					return false
				}
			},
			nil,
			func(think string) bool {
				select {
				case ch <- StreamChunk{Think: think}:
					return true
				case <-ctx.Done():
					return false
				}
			},
		)
		if err != nil {
			log.Printf("ollama: chat stream failed: %v", err)
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
func (p *OllamaProvider) ChatTools(ctx context.Context, req ChatRequest) (<-chan ToolEvent, error) {
	payload, err := p.chatPayload(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, http.MethodPost, ollamaChatPath, payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, toolsError("ollama", resp.StatusCode, resp.Status, string(msg))
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
		err := p.readChatStream(ctx, resp,
			func(delta string) bool { return send(ToolEvent{Delta: delta}) },
			func(call ToolCall) bool { return send(ToolEvent{ToolCall: &call}) },
			func(think string) bool { return send(ToolEvent{Think: think}) },
		)
		if err != nil {
			log.Printf("ollama: tool chat stream failed: %v", err)
			send(ToolEvent{Done: true})
		}
	}()
	return events, nil
}

// readChatStream consumes the NDJSON body of a streamed /api/chat response.
func (p *OllamaProvider) readChatStream(ctx context.Context, resp *http.Response, onDelta func(string) bool, onTool func(ToolCall) bool, onThink func(string) bool) error {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj ollamaStreamLine
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return fmt.Errorf("ollama: malformed stream line: %w", err)
		}
		if obj.Error != "" {
			return errors.New(obj.Error)
		}
		if obj.Message.Think != "" && onThink != nil {
			if !onThink(obj.Message.Think) {
				return ctx.Err()
			}
		}
		if obj.Message.Content != "" && onDelta != nil {
			if !onDelta(obj.Message.Content) {
				return ctx.Err()
			}
		}
		if onTool != nil {
			for _, c := range obj.Message.Calls {
				call := ToolCall{Name: c.Function.Name, Arguments: c.Function.Arguments}
				if !onTool(call) {
					return ctx.Err()
				}
			}
		}
	}
	return sc.Err()
}

// Complete performs a single completion and parses a structured reply.
func (p *OllamaProvider) Complete(ctx context.Context, req CompletionRequest) (*Completion, error) {
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

	resp, err := p.do(ctx, http.MethodPost, ollamaChatPath, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama: chat: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	var obj struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("ollama: decode chat response: %w", err)
	}
	if obj.Error != "" {
		return nil, fmt.Errorf("ollama: chat: %s", obj.Error)
	}
	return parseCompletion(obj.Message.Content)
}

// ollamaEmbedResponse is the wire format of POST /api/embed.
type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

// Embed returns one embedding vector per input text. The texts are sent as a
// single batched request; Ollama accepts an array for "input".
func (p *OllamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	model := p.embedModel()
	if model == "" {
		return nil, errors.New("ollama: no embedding model configured")
	}
	payload := map[string]any{
		"model":      model,
		"input":      texts,
		"truncate":   true,
		"keep_alive": p.keepAlive(),
	}
	resp, err := p.do(ctx, http.MethodPost, ollamaEmbedPath, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama: embed: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var obj ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("ollama: decode embed response: %w", err)
	}
	if obj.Error != "" {
		return nil, fmt.Errorf("ollama: embed: %s", obj.Error)
	}
	if len(obj.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama: embed: got %d vectors for %d inputs", len(obj.Embeddings), len(texts))
	}
	return obj.Embeddings, nil
}

// ollamaTagsResponse is the wire format of GET /api/tags.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	Error string `json:"error"`
}

// Models lists the models installed on the Ollama server.
func (p *OllamaProvider) Models(ctx context.Context) ([]string, error) {
	resp, err := p.do(ctx, http.MethodGet, ollamaTagsPath, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama: tags: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var obj ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("ollama: decode tags response: %w", err)
	}
	if obj.Error != "" {
		return nil, fmt.Errorf("ollama: tags: %s", obj.Error)
	}
	names := make([]string, 0, len(obj.Models))
	for _, m := range obj.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// Health probes the Ollama server with a cheap GET /api/tags.
func (p *OllamaProvider) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ollamaHealthTimeout)
	defer cancel()
	resp, err := p.do(ctx, http.MethodGet, ollamaTagsPath, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("ollama: health: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// SupportsThink reports whether the given model advertises reasoning
// ("thinking" among the capabilities /api/show lists).
func (p *OllamaProvider) SupportsThink(ctx context.Context, model string) (bool, error) {
	return p.hasCapability(ctx, model, "thinking")
}

// SupportsTools reports whether the given model can call tools ("tools"
// among its capabilities). A model without it cannot search the web or read
// files during a chat, and Ollama refuses a request that offers it tools.
// See https://github.com/ollama/ollama/blob/main/docs/api.md#show-model-information
func (p *OllamaProvider) SupportsTools(ctx context.Context, model string) (bool, error) {
	return p.hasCapability(ctx, model, "tools")
}

// hasCapability asks /api/show for a model's capabilities.
func (p *OllamaProvider) hasCapability(ctx context.Context, model, capability string) (bool, error) {
	if model == "" {
		model = p.opts.chatModel()
	}
	if model == "" {
		return false, errors.New("ollama: no model configured")
	}
	resp, err := p.do(ctx, http.MethodPost, ollamaShowPath, map[string]any{"model": model})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return false, fmt.Errorf("ollama: show: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var obj struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return false, fmt.Errorf("ollama: decode show response: %w", err)
	}
	for _, c := range obj.Capabilities {
		if strings.EqualFold(c, capability) {
			return true, nil
		}
	}
	return false, nil
}

// parseCompletion turns a raw model reply into a Completion, parsing JSON
// objects when the model produced one.
func parseCompletion(text string) (*Completion, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("llm: empty completion")
	}
	comp := &Completion{Text: text}
	payload := stripCodeFence(trimmed)
	if !strings.HasPrefix(payload, "{") {
		return comp, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return nil, fmt.Errorf("llm: parse structured completion: %w", err)
	}
	comp.JSON = obj
	return comp, nil
}

// stripCodeFence removes a markdown code fence around a JSON payload.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
