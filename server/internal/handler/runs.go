package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	// maxScript is the longest script a run record stores whole.
	maxScript = 256 << 10
	// maxStudyPackages bounds the study's own part of the package list a
	// record keeps inline.
	maxStudyPackages = 64 << 10
)

func hexSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// studyPackages is the part of a package list after its `# <language> study`
// line: what the study added to the language's base.
func studyPackages(packages, language string) string {
	_, after, ok := strings.Cut("\n"+packages, "\n# "+language+" study\n")
	if !ok {
		return ""
	}
	kept, _ := flow.Excerpt(after, maxStudyPackages)
	return kept
}

// postRun records code the daemon's door ran on the run's behalf, a cell or a
// script, as the study's next run record. The whole output, when the body
// keeps only part of it, and the package list go to blobs first; only the
// blob.created events committed with the record make them readable.
func (h *H) postRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tool       string `json:"tool"`
		Language   string `json:"language"`
		Code       string `json:"code"`
		Path       string `json:"path"`
		Script     string `json:"script"`
		Output     string `json:"output"`
		Error      string `json:"error"`
		ExitStatus int    `json:"exit_status"`
		Packages   string `json:"packages"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "tool, language and output required")
		return
	}
	if in.Tool != "run_code" && in.Tool != "run_script" {
		writeErr(w, 400, "tool must be run_code or run_script")
		return
	}
	switch in.Language {
	case "python", "r", "julia":
	default:
		writeErr(w, 400, "language must be python, r or julia")
		return
	}
	if len(in.Script) > maxScript {
		writeErr(w, 400, "the script is over 256 KiB")
		return
	}
	role := middleware.RoleFrom(r.Context())
	if role.CardID == uuid.Nil {
		// a run with no card keeps no run record
		if err := h.roleTx(r, func(*store.Tx, middleware.Role) error { return nil }); err != nil {
			roleError(w, r, err)
			return
		}
		writeJSON(w, 201, map[string]string{})
		return
	}
	output, truncated := sources.Cut(in.Output)
	errText, _ := sources.Cut(in.Error)
	body := map[string]any{"tool": in.Tool, "language": in.Language, "exit_status": in.ExitStatus,
		"output": output, "output_sha256": hexSHA(in.Output), "truncated": truncated, "error": errText,
		"packages_sha256": hexSHA(in.Packages), "packages_study": studyPackages(in.Packages, in.Language),
		"at": time.Now().UTC().Format(time.RFC3339), "agent_run_id": role.AgentRunID}
	if in.Tool == "run_code" {
		body["code"] = in.Code
	} else {
		body["path"], body["script"], body["script_sha256"] = in.Path, in.Script, hexSHA(in.Script)
	}
	blobs := map[string]string{} // sha256 → text
	outputKey := ""
	if truncated {
		blobs[body["output_sha256"].(string)] = in.Output
		outputKey = blobKey(role, body["output_sha256"].(string))
	}
	if in.Packages != "" {
		blobs[body["packages_sha256"].(string)] = in.Packages
	}
	for sum, text := range blobs {
		if err := h.Blobs.Put(r.Context(), blobKey(role, sum), bytes.NewReader([]byte(text)), int64(len(text))); err != nil {
			fail(w, r, err)
			return
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		fail(w, r, err)
		return
	}
	var key string
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		// the card lock roleTx took keeps two runs from taking one number
		k, err := records.NextRunKey(r.Context(), tx, role.CardID)
		if err != nil {
			return err
		}
		key = k
		actor := store.Actor{Type: "role", ID: role.AgentRunID}
		for sum, text := range blobs {
			if err := tx.Emit(r.Context(), "blob.created", &role.CardID, actor,
				map[string]any{"blob_key": blobKey(role, sum), "sha256": sum, "size": int64(len(text))}); err != nil {
				return err
			}
		}
		rec, err := tx.Q.InsertRecord(r.Context(), db.InsertRecordParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID,
			BoardColumn: role.Column, Round: role.Round, Kind: "run", Role: role.Role, Key: key, Body: b,
			BlobKey: outputKey, AgentRunID: &role.AgentRunID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "record.created", &role.CardID, actor,
			map[string]any{"record_id": rec.ID, "kind": "run", "key": key, "role": role.Role})
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]string{"key": key})
}

// listRuns lists the study's run records in the run's column, each output cut
// to an excerpt.
func (h *H) listRuns(w http.ResponseWriter, r *http.Request) {
	var recs []db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		recs, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column})
		return err
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, rec := range recs {
		if rec.Kind != "run" {
			continue
		}
		var body map[string]any
		if json.Unmarshal(rec.Body, &body) != nil {
			continue
		}
		if o, ok := body["output"].(string); ok {
			if cut, more := flow.Excerpt(o, 2000); more {
				body["output"], body["truncated"] = cut, true
			}
		}
		body["key"] = rec.Key
		out = append(out, body)
	}
	writeJSON(w, 200, out)
}
