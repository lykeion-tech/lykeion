package handler

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"lykeion/server/internal/workflow"
)

// The library is the same for everyone, so these routes need a session but no
// workspace.
func (h *H) listWorkflows(w http.ResponseWriter, r *http.Request) {
	all := workflow.All()
	type summary struct {
		workflow.Workflow
		Files *struct{} `json:"files,omitempty"` // shadows the embedded list so it is left out
	}
	out := make([]summary, len(all))
	for i, wf := range all {
		out[i] = summary{Workflow: wf}
	}
	writeJSON(w, http.StatusOK, map[string]any{"release": workflow.Release(), "workflows": out})
}

func (h *H) getWorkflow(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	wf, ok := workflow.Get(slug)
	if !ok {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	readme, _ := workflow.ReadFile(slug, "README.md")
	instruction, _ := workflow.ReadFile(slug, "instruction.md")
	writeJSON(w, http.StatusOK, struct {
		workflow.Workflow
		Readme      string `json:"readme"`
		Instruction string `json:"instruction"`
	}{wf, string(readme), string(instruction)})
}

func (h *H) getWorkflowFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	b, err := workflow.ReadFile(chi.URLParam(r, "slug"), p)
	if errors.Is(err, fs.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the file")
		return
	}
	ct := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(b)
}
