package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// ollamaTagsEntry is one entry of GET /api/tags.
type ollamaTagsEntry struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
	Details    struct {
		Format            string `json:"format"`
		Family            string `json:"family"`
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
}

// ListModels returns the models installed on the Ollama server, with the
// detail the model picker needs.
//
// The list comes from a single GET /api/tags call, so it stays cheap even
// when many models are installed.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]model.ModelInfo, error) {
	resp, err := p.do(ctx, http.MethodGet, ollamaTagsPath, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama: tags: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var obj struct {
		Models []ollamaTagsEntry `json:"models"`
		Error  string            `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("ollama: decode tags response: %w", err)
	}
	if obj.Error != "" {
		return nil, fmt.Errorf("ollama: tags: %s", obj.Error)
	}
	out := make([]model.ModelInfo, 0, len(obj.Models))
	for _, m := range obj.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" {
			continue
		}
		out = append(out, model.ModelInfo{
			Name:              name,
			Size:              m.Size,
			SizeText:          humanizeBytes(m.Size),
			Family:            m.Details.Family,
			ParameterSize:     m.Details.ParameterSize,
			QuantizationLevel: m.Details.QuantizationLevel,
			ModifiedAt:        m.ModifiedAt,
		})
	}
	return out, nil
}

// Unload releases the given models from the Ollama server's memory.
//
// Ollama keeps a model resident until no request has used it for the
// keep-alive window. Sending a request with keep_alive set to zero
// unloads the model immediately, which is what the "release memory" action
// in the model picker does.
//
// An empty list unloads the models the server is currently configured to
// use.
func (p *OllamaProvider) Unload(ctx context.Context, names []string) error {
	if len(names) == 0 {
		names = []string{p.chatModel(), p.embedModel()}
	}
	var firstErr error
	for _, name := range names {
		if name == "" {
			continue
		}
		payload := map[string]any{
			"model":      name,
			"keep_alive": 0,
		}
		resp, err := p.do(ctx, http.MethodPost, ollamaGeneratePath, payload)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		status := resp.StatusCode
		resp.Body.Close()
		if status != http.StatusOK {
			if firstErr == nil {
				firstErr = fmt.Errorf("ollama: unload %s: %s: %s", name, resp.Status, strings.TrimSpace(string(msg)))
			}
		}
	}
	return firstErr
}

// humanizeBytes renders a byte count the way the model picker shows it.
func humanizeBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
