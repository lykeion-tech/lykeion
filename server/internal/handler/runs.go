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
	// maxScript is the longest script or cell a run record stores whole.
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
		Tool         string            `json:"tool"`
		Language     string            `json:"language"`
		Code         string            `json:"code"`
		Path         string            `json:"path"`
		Script       string            `json:"script"`
		Output       string            `json:"output"`
		Error        string            `json:"error"`
		ExitStatus   *int              `json:"exit_status"`
		Packages     string            `json:"packages"`
		OutputHashes map[string]string `json:"output_hashes"`
		// Older daemons still send these; they are read and dropped.
		Results      json.RawMessage `json:"results"`
		ResultsError string          `json:"results_error"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid run: "+err.Error())
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
	if len(in.Code) > maxScript {
		writeErr(w, 400, "the code is over 256 KiB")
		return
	}
	if in.ExitStatus == nil {
		writeErr(w, 400, "exit_status is required")
		return
	}
	if in.OutputHashes != nil {
		if in.Tool != "run_script" || *in.ExitStatus != 0 {
			writeErr(w, 400, "only successful script runs can record output hashes")
			return
		}
		if len(in.OutputHashes) > records.MaxScriptOutputPaths {
			writeErr(w, 400, "too many script output hashes")
			return
		}
		for name, hash := range in.OutputHashes {
			if !records.ValidScriptOutputPath(name) || len(hash) != 2*sha256.Size || strings.ToLower(hash) != hash {
				writeErr(w, 400, "invalid script output path or SHA-256")
				return
			}
			if _, err := hex.DecodeString(hash); err != nil {
				writeErr(w, 400, "invalid script output SHA-256")
				return
			}
		}
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
	errText, errCut := sources.Cut(in.Error)
	outputSum, errSum := hexSHA(in.Output), hexSHA(in.Error)
	body := map[string]any{"tool": in.Tool, "language": in.Language, "exit_status": *in.ExitStatus,
		"output": output, "output_sha256": outputSum, "truncated": truncated,
		"error": errText, "error_sha256": errSum, "error_truncated": errCut,
		"packages_study": studyPackages(in.Packages, in.Language),
		"at":             time.Now().UTC().Format(time.RFC3339), "agent_run_id": role.AgentRunID}
	if in.Tool == "run_code" {
		body["code"] = in.Code
	} else {
		body["path"], body["script"], body["script_sha256"] = in.Path, in.Script, hexSHA(in.Script)
		if in.OutputHashes != nil {
			body["output_hashes"] = in.OutputHashes
		}
	}
	// the whole texts a body keeps only part of, and the package list, by sha256
	blobs := map[string]string{}
	outputKey := ""
	if truncated {
		blobs[outputSum] = in.Output
		outputKey = blobKey(role, outputSum)
	}
	if errCut {
		blobs[errSum] = in.Error
		body["error_blob"] = blobKey(role, errSum)
	}
	if in.Packages != "" {
		sum := hexSHA(in.Packages)
		blobs[sum] = in.Packages
		body["packages_sha256"] = sum
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

// listRuns lists the study's run records in the run's column: what each ran
// and how it ended, its output cut to an excerpt.
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
		var body struct {
			Tool       string `json:"tool"`
			Language   string `json:"language"`
			Path       string `json:"path"`
			ExitStatus int    `json:"exit_status"`
			At         string `json:"at"`
			Output     string `json:"output"`
			Truncated  bool   `json:"truncated"`
		}
		if json.Unmarshal(rec.Body, &body) != nil {
			continue
		}
		output, more := flow.Excerpt(body.Output, 2000)
		run := map[string]any{"key": rec.Key, "tool": body.Tool, "language": body.Language, "exit_status": body.ExitStatus,
			"at": body.At, "output": output, "truncated": body.Truncated || more}
		if body.Path != "" {
			run["path"] = body.Path
		}
		out = append(out, run)
	}
	writeJSON(w, 200, out)
}
