package server

import (
	"context"
	"net/http"

	"github.com/tlmcguire/aisai/internal/model"
)

// ModelUnloader is implemented by providers that can release models from
// memory on demand.
//
// Ollama keeps a model resident until no request has used it for the
// keep-alive window. This optional interface lets the desktop app ask for
// the memory back immediately instead of waiting.
type ModelUnloader interface {
	// Unload releases the named models from memory. An empty list means
	// "the models the server is using".
	Unload(ctx context.Context, names []string) error
}

// modelState is the response body of the model endpoints.
//
// It carries both the models that are installed and the ones that are
// currently selected, so the renderer can render the picker from a single
// request.
type modelState struct {
	Models     []model.ModelInfo `json:"models"`
	ChatModel  string            `json:"chatModel"`
	ScanModel  string            `json:"scanModel"`
	EmbedModel string            `json:"embedModel"`
	NumCtx     int               `json:"numCtx"`
	KeepAlive  string            `json:"keepAlive"`
	// Error explains why the installed models could not be listed. The
	// selected models are still reported, because they remain in use.
	Error string `json:"error,omitempty"`
}

// modelLister is implemented by providers that can describe the models
// they have installed.
type modelLister interface {
	ListModels(ctx context.Context) ([]model.ModelInfo, error)
}

// handleModels lists the installed models and the selected ones.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.modelState(r.Context()))
}

// handleSetModels changes the models the server uses.
//
// The change takes effect immediately: the provider, the detection pipeline
// and the chat all read the model names from the shared model set on every
// request. It is also written back to the configuration file, so the
// choice survives a restart.
func (s *Server) handleSetModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatModel  string `json:"chatModel"`
		ScanModel  string `json:"scanModel"`
		EmbedModel string `json:"embedModel"`
		NumCtx     int    `json:"numCtx"`
		KeepAlive  string `json:"keepAlive"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	// An empty name means "leave this one alone", so the renderer can
	// send only the fields the user changed.
	s.models.Set(req.ChatModel, req.ScanModel, req.EmbedModel)
	if req.NumCtx > 0 {
		s.models.SetNumCtx(req.NumCtx)
	}
	if req.KeepAlive != "" {
		s.models.SetKeepAlive(req.KeepAlive)
	}

	// The configuration file mirrors the runtime state.
	s.cfg.ChatModel = s.models.Chat()
	s.cfg.ScanModel = s.models.Scan()
	s.cfg.EmbedModel = s.models.Embed()
	s.cfg.NumCtx = s.models.NumCtx()
	s.cfg.KeepAlive = s.models.KeepAlive()

	if err := s.cfg.Save(); err != nil {
		// The models are already changed and usable; only the
		// persistence failed. Report it without failing the request.
		writeJSON(w, http.StatusOK, mergeModelError(s.modelState(r.Context()),
			"the models were applied but the configuration file could not be written: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, s.modelState(r.Context()))
}

// handleUnloadModels releases models from the provider's memory.
func (s *Server) handleUnloadModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Models []string `json:"models"`
	}
	// A body is optional: an empty request unloads the models in use.
	if r.ContentLength > 0 && !decodeBody(w, r, &req) {
		return
	}

	unloader, ok := s.llm.(ModelUnloader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "not_implemented",
			"this provider cannot release models from memory")
		return
	}

	names := req.Models
	if len(names) == 0 {
		names = []string{s.models.Chat(), s.models.Scan(), s.models.Embed()}
	}
	if err := unloader.Unload(r.Context(), names); err != nil {
		writeError(w, http.StatusBadGateway, "unload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unloaded": names})
}

// modelState builds the response of the model endpoints.
func (s *Server) modelState(ctx context.Context) modelState {
	state := modelState{
		ChatModel:  s.models.Chat(),
		ScanModel:  s.models.Scan(),
		EmbedModel: s.models.Embed(),
		NumCtx:     s.models.NumCtx(),
		KeepAlive:  s.models.KeepAlive(),
		Models:     []model.ModelInfo{},
	}
	lister, ok := s.llm.(modelLister)
	if !ok {
		return state
	}
	infos, err := lister.ListModels(ctx)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	state.Models = infos
	return state
}

// mergeModelError returns a copy of the state with the error attached.
func mergeModelError(state modelState, message string) modelState {
	state.Error = message
	return state
}
