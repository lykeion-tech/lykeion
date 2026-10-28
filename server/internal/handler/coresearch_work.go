package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// coresearchRunTx retains the run identity throughout its cardless transaction.
func (h *H) coresearchRunTx(r *http.Request, write bool, fn func(*store.Tx, db.CoresearchChat, uuid.UUID) error) error {
	role := middleware.RoleFrom(r.Context())
	return h.Store.InWorkspace(r.Context(), role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error {
		if _, e := tx.Q.LockWorkspaceForMutation(r.Context(), tx.WS); e != nil {
			return e
		}
		if e := middleware.LockRole(r.Context(), tx, role, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); e != nil {
			return pgx.ErrNoRows
		}
		run, e := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: tx.WS, ID: role.AgentRunID})
		if e != nil {
			return e
		}
		if run.Kind != "coresearch" || run.CoresearchChatID == nil || run.RequesterID == nil {
			return pgx.ErrNoRows
		}
		if _, e = tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: tx.WS, UserID: *run.RequesterID}); e != nil {
			return e
		}
		c, e := tx.Q.CoresearchChatByID(r.Context(), db.CoresearchChatByIDParams{WorkspaceID: tx.WS, ID: *run.CoresearchChatID})
		if e != nil {
			return e
		}
		if c.OwnerID != *run.RequesterID {
			return pgx.ErrNoRows
		}
		if _, e = tx.Q.CoresearchMember(r.Context(), db.CoresearchMemberParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, UserID: c.OwnerID}); e != nil {
			return e
		}
		p, e := tx.Q.LockCoresearchProject(r.Context(), db.LockCoresearchProjectParams{WorkspaceID: tx.WS, ID: c.ProjectID})
		if e != nil {
			return e
		}
		if write && p.ArchivedAt != nil {
			return coresearch.ErrArchived
		}
		c, e = tx.Q.LockCoresearchChat(r.Context(), db.LockCoresearchChatParams{WorkspaceID: tx.WS, ID: c.ID})
		if e != nil {
			return e
		}
		return fn(tx, c, role.AgentRunID)
	})
}
func (h *H) coresearchWorkTx(r *http.Request, write bool, chatID uuid.UUID, fn func(*store.Tx, db.CoresearchChat, *uuid.UUID) error) error {
	if middleware.RoleFrom(r.Context()).AgentRunID != uuid.Nil {
		return h.coresearchRunTx(r, write, func(tx *store.Tx, c db.CoresearchChat, run uuid.UUID) error { return fn(tx, c, &run) })
	}
	return h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		if chatID != uuid.Nil {
			chi.RouteContext(r.Context()).URLParams.Add("chat", chatID.String())
		}
		c, e := coresearchChat(r, tx, ws, who, write, write)
		if e != nil {
			return e
		}
		return fn(tx, c, nil)
	})
}
func (h *H) coresearchCells(w http.ResponseWriter, r *http.Request) {
	var out any
	e := h.coresearchWorkTx(r, r.Method == "POST", uuid.Nil, func(tx *store.Tx, c db.CoresearchChat, run *uuid.UUID) error {
		if r.Method == "GET" {
			if raw := chi.URLParam(r, "id"); raw != "" {
				id, e := uuid.Parse(raw)
				if e != nil {
					return pgx.ErrNoRows
				}
				row, e := tx.Q.GetCoresearchCell(r.Context(), db.GetCoresearchCellParams{WorkspaceID: tx.WS, ID: id})
				if e != nil {
					return e
				}
				if row.ChatID != c.ID {
					return pgx.ErrNoRows
				}
				out = row
				return nil
			}
			rows, e := tx.Q.ListCoresearchCells(r.Context(), db.ListCoresearchCellsParams{WorkspaceID: tx.WS, ChatID: c.ID})
			if rows == nil {
				rows = []db.CoresearchCell{}
			}
			out = rows
			return e
		}
		var in struct {
			Kernel string `json:"kernel"`
			Source string `json:"source"`
		}
		if e := readJSON(r, &in); e != nil {
			return cardInputError("kernel and source required")
		}
		if in.Kernel == "" || utf8.RuneCountInString(in.Kernel) > 128 || len(in.Source) > 262144 {
			return cardInputError("invalid kernel or source")
		}
		row, e := coresearch.EnqueueCell(r.Context(), tx, c, c.OwnerID, run, in.Kernel, in.Source)
		out = row
		return e
	})
	if !coresearchAnswer(w, r, e) {
		return
	}
	if cell, ok := out.(db.CoresearchCell); ok && r.Method == "GET" && cell.OutputsBlob != nil {
		rc, e := h.Blobs.Get(r.Context(), *cell.OutputsBlob)
		if e != nil {
			logoFail(w, r, e)
			return
		}
		b, e := io.ReadAll(io.LimitReader(rc, coresearch.MaxCellOutputBytes+1))
		rc.Close()
		if e != nil || len(b) > coresearch.MaxCellOutputBytes || !json.Valid(b) {
			fail(w, r, errors.New("invalid stored cell outputs"))
			return
		}
		cell.Outputs = b
		out = cell
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	writeJSON(w, status, out)
}
func (h *H) coresearchKernel(w http.ResponseWriter, r *http.Request) {
	e := h.coresearchWorkTx(r, true, uuid.Nil, func(tx *store.Tx, c db.CoresearchChat, _ *uuid.UUID) error {
		var in struct {
			Op string `json:"op"`
		}
		if readJSON(r, &in) != nil || !slices.Contains([]string{"interrupt", "restart"}, in.Op) {
			return cardInputError("op must be interrupt or restart")
		}
		if e := tx.Q.RequestCoresearchKernel(r.Context(), db.RequestCoresearchKernelParams{WorkspaceID: tx.WS, ID: c.ID, Op: &in.Op}); e != nil {
			return e
		}
		if e := coresearch.Changed(r.Context(), tx, "chat", c, c.ID); e != nil {
			return e
		}
		return coresearch.Wake(r.Context(), tx, c)
	})
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
func (h *H) coresearchRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" && middleware.RoleFrom(r.Context()).AgentRunID == uuid.Nil {
		e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
			_, e := coresearchProject(r, tx, ws, who, true, false)
			return e
		})
		if !coresearchAnswer(w, r, e) {
			return
		}
	}

	var out any
	var e error
	if r.Method == "POST" {
		var in struct {
			ChatID     uuid.UUID `json:"chat_id"`
			Script     string    `json:"script"`
			Inputs     []string  `json:"inputs"`
			RerunOfKey string    `json:"rerun_of_key"`
		}
		if !strings.HasSuffix(r.URL.Path, "/rerun") {
			if e = readJSON(r, &in); e != nil {
				coresearchAnswer(w, r, cardInputError("invalid run"))
				return
			}
		}
		if key := chi.URLParam(r, "key"); key != "" {
			in.RerunOfKey = key
			e = h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
				p, e := coresearchProject(r, tx, ws, who, true, false)
				if e != nil {
					return e
				}
				old, e := tx.Q.GetCoresearchRunKey(r.Context(), db.GetCoresearchRunKeyParams{WorkspaceID: ws, ProjectID: p.ID, Key: key})
				in.ChatID = old.ChatID
				return e
			})
			if e != nil {
				coresearchAnswer(w, r, e)
				return
			}
		}
		e = h.coresearchWorkTx(r, true, in.ChatID, func(tx *store.Tx, c db.CoresearchChat, agent *uuid.UUID) error {
			var old *db.CoresearchRun
			if in.RerunOfKey != "" {
				row, e := tx.Q.GetCoresearchRunKey(r.Context(), db.GetCoresearchRunKeyParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, Key: in.RerunOfKey})
				if e != nil {
					return e
				}
				if row.ChatID != c.ID {
					return pgx.ErrNoRows
				}
				old = &row
			}
			row, e := coresearch.EnqueueRun(r.Context(), tx, c, c.OwnerID, agent, in.Script, in.Inputs, old)
			out = row
			if e != nil && !errors.Is(e, coresearch.ErrArchived) {
				return cardInputError(e.Error())
			}
			return e
		})
	} else if middleware.RoleFrom(r.Context()).AgentRunID != uuid.Nil {
		e = h.coresearchRunTx(r, false, func(tx *store.Tx, c db.CoresearchChat, _ uuid.UUID) error {
			row, e := tx.Q.GetCoresearchRunKey(r.Context(), db.GetCoresearchRunKeyParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, Key: chi.URLParam(r, "key")})
			if e == nil && row.ChatID != c.ID {
				return pgx.ErrNoRows
			}
			if e != nil {
				return e
			}
			out, e = coresearchRunWithOutputs(r.Context(), tx, row)
			return e
		})
	} else {
		e = h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
			p, e := coresearchProject(r, tx, ws, who, false, false)
			if e != nil {
				return e
			}
			if key := chi.URLParam(r, "key"); key != "" {
				row, err := tx.Q.GetCoresearchRunKey(r.Context(), db.GetCoresearchRunKeyParams{WorkspaceID: ws, ProjectID: p.ID, Key: key})
				if err != nil {
					return err
				}
				out, e = coresearchRunWithOutputs(r.Context(), tx, row)
				return e
			}
			rows, e := tx.Q.ListCoresearchRuns(r.Context(), db.ListCoresearchRunsParams{WorkspaceID: ws, ProjectID: p.ID})
			if rows == nil {
				rows = []db.CoresearchRun{}
			}
			out = rows
			return e
		})
	}
	if coresearchAnswer(w, r, e) {
		status := 200
		if r.Method == "POST" {
			status = 201
		}
		writeJSON(w, status, out)
	}
}
func (h *H) coresearchEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" && middleware.RoleFrom(r.Context()).AgentRunID == uuid.Nil {
		e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
			_, e := coresearchProject(r, tx, ws, who, true, false)
			return e
		})
		if !coresearchAnswer(w, r, e) {
			return
		}
	}

	var out any
	var e error
	if r.Method == "POST" {
		var in struct {
			ChatID uuid.UUID `json:"chat_id"`
			Python *string   `json:"python"`
			Conda  []string  `json:"conda"`
		}
		if readJSON(r, &in) != nil {
			coresearchAnswer(w, r, cardInputError("invalid environment"))
			return
		}
		e = h.coresearchWorkTx(r, true, in.ChatID, func(tx *store.Tx, c db.CoresearchChat, _ *uuid.UUID) error {
			if in.Python != nil && *in.Python != "" && !service.ValidPythonVersion(*in.Python) || len(in.Conda) > 256 {
				return cardInputError("environment request too large")
			}
			for _, pkg := range in.Conda {
				if len(pkg) > 512 || strings.TrimSpace(pkg) == "" {
					return cardInputError("invalid conda package")
				}
			}
			if in.Conda == nil {
				in.Conda = []string{}
			}
			row, e := tx.Q.EnqueueCoresearchSetup(r.Context(), db.EnqueueCoresearchSetupParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, ChatID: c.ID, Python: in.Python, Conda: in.Conda, RequestedBy: c.OwnerID})
			out = row
			if e != nil {
				return e
			}
			if e = coresearch.Changed(r.Context(), tx, "setup", c, row.ID); e != nil {
				return e
			}
			return coresearch.Wake(r.Context(), tx, c)
		})
	} else if middleware.RoleFrom(r.Context()).AgentRunID != uuid.Nil {
		e = h.coresearchRunTx(r, false, func(tx *store.Tx, c db.CoresearchChat, _ uuid.UUID) error {
			id, e := coresearchID(r, "id")
			if e != nil {
				return e
			}
			row, e := tx.Q.GetCoresearchSetup(r.Context(), db.GetCoresearchSetupParams{WorkspaceID: tx.WS, ID: id})
			if e == nil && row.ChatID != c.ID {
				return pgx.ErrNoRows
			}
			out = row
			return e
		})
	} else {
		e = h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
			p, e := coresearchProject(r, tx, ws, who, false, false)
			if e != nil {
				return e
			}
			rows, e := tx.Q.ListCoresearchSetups(r.Context(), db.ListCoresearchSetupsParams{WorkspaceID: ws, ProjectID: p.ID})
			if rows == nil {
				rows = []db.CoresearchSetup{}
			}
			out = map[string]any{"python_version": p.PythonVersion, "setups": rows}
			return e
		})
	}
	if coresearchAnswer(w, r, e) {
		status := 200
		if r.Method == "POST" {
			status = 201
		}
		writeJSON(w, status, out)
	}
}
func (h *H) daemonCoresearchClaim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DaemonID string               `json:"daemon_id"`
		Runtimes []service.RuntimeRef `json:"runtimes"`
	}
	if readJSON(r, &in) != nil || in.DaemonID == "" || len(in.Runtimes) == 0 || len(in.Runtimes) > 64 {
		writeErr(w, 400, "daemon_id and runtimes required")
		return
	}
	out := coresearch.EmptyWork()
	groups := map[uuid.UUID][]uuid.UUID{}
	for _, ref := range in.Runtimes {
		groups[ref.WorkspaceID] = append(groups[ref.WorkspaceID], ref.RuntimeID)
	}
	for ws, ids := range groups {
		e := h.Store.InWorkspace(r.Context(), ws, user(r), func(tx *store.Tx) error {
			if _, e := tx.Q.LockWorkspaceForMutation(r.Context(), ws); e != nil {
				return e
			}
			if _, e := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: user(r)}); e != nil {
				return e
			}
			got, e := coresearch.Claim(r.Context(), tx, user(r), in.DaemonID, ids)
			out.Cells = append(out.Cells, got.Cells...)
			out.Runs = append(out.Runs, got.Runs...)
			out.Setups = append(out.Setups, got.Setups...)
			out.Controls = append(out.Controls, got.Controls...)
			return e
		})
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if !coresearchAnswer(w, r, e) {
			return
		}
	}
	writeJSON(w, 200, out)
}

// Every daemon continuation supplies its bound daemon id and workspace header.
func (h *H) coresearchDaemonTx(r *http.Request, fn func(*store.Tx, string) error) error {
	ws, e := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if e != nil {
		return pgx.ErrNoRows
	}
	daemon := r.Header.Get("X-Daemon-ID")
	if daemon == "" {
		daemon = r.URL.Query().Get("daemon_id")
	}
	if daemon == "" {
		return pgx.ErrNoRows
	}
	return h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
		ok, e := tx.Q.CoresearchOwnedDaemon(r.Context(), db.CoresearchOwnedDaemonParams{WorkspaceID: ws, DaemonID: daemon, OwnerID: user(r)})
		if e != nil {
			return e
		}
		if !ok {
			return pgx.ErrNoRows
		}
		return fn(tx, daemon)
	})
}
func daemonCoresearchChat(r *http.Request, tx *store.Tx, id uuid.UUID, daemon string) (db.CoresearchChat, error) {
	c, e := tx.Q.CoresearchChatByID(r.Context(), db.CoresearchChatByIDParams{WorkspaceID: tx.WS, ID: id})
	if e != nil {
		return c, e
	}
	if c.OwnerID != user(r) || c.DaemonID == nil || *c.DaemonID != daemon {
		return c, pgx.ErrNoRows
	}
	if _, e = tx.Q.LockCoresearchProject(r.Context(), db.LockCoresearchProjectParams{WorkspaceID: tx.WS, ID: c.ProjectID}); e != nil {
		return c, e
	}
	return tx.Q.LockCoresearchChat(r.Context(), db.LockCoresearchChatParams{WorkspaceID: tx.WS, ID: c.ID})
}
func (h *H) daemonCoresearchCell(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "id")
	if !coresearchAnswer(w, r, e) {
		return
	}
	var in struct {
		Status         string          `json:"status"`
		ExecutionCount *int32          `json:"execution_count"`
		Outputs        json.RawMessage `json:"outputs"`
	}
	finish := strings.HasSuffix(r.URL.Path, "/finish")
	var blob *string
	var receipt *string
	var c db.CoresearchChat
	check := func(tx *store.Tx, daemon string) error {
		cell, e := tx.Q.GetCoresearchCell(r.Context(), db.GetCoresearchCellParams{WorkspaceID: tx.WS, ID: id})
		if e != nil {
			return e
		}
		c, e = daemonCoresearchChat(r, tx, cell.ChatID, daemon)
		if e != nil {
			return e
		}
		if cell.ClaimedDaemonID == nil || *cell.ClaimedDaemonID != daemon {
			return pgx.ErrNoRows
		}
		receipt = cell.ReceiptSha
		return coresearchClaim(r, cell.ClaimToken, cell.LeaseExpiresAt, cell.Status, cell.ReceiptSha, finish)
	}
	e = h.coresearchDaemonTx(r, check)
	if !coresearchAnswer(w, r, e) {
		return
	}
	if finish {
		if readJSON(r, &in) != nil || !slices.Contains([]string{"ok", "error", "interrupted"}, in.Status) || !json.Valid(in.Outputs) {
			writeErr(w, 400, "invalid cell result")
			return
		}
		if len(in.Outputs) > coresearch.MaxCellOutputBytes {
			writeErr(w, 413, "cell outputs exceed 20 MiB")
			return
		}
	}
	digest := coresearchReceipt(in)
	if receipt != nil {
		if *receipt != digest {
			coresearchAnswer(w, r, service.ErrConflict)
			return
		}
		w.WriteHeader(204)
		return
	}
	finishCell := func() error {
		if e := h.coresearchDaemonTx(r, check); e != nil {
			return e
		}
		if receipt != nil {
			if *receipt != digest {
				return service.ErrConflict
			}
			return nil
		}
		if finish && len(in.Outputs) > coresearch.InlineCellOutputBytes {
			key := c.WorkspaceID.String() + "/coresearch/cells/" + id.String() + ".json"
			if e := h.Blobs.Put(r.Context(), key, bytes.NewReader(in.Outputs), int64(len(in.Outputs))); e != nil {
				return e
			}
			blob = &key
		}
		return h.coresearchDaemonTx(r, func(tx *store.Tx, daemon string) error {
			if e := check(tx, daemon); e != nil {
				return e
			}
			if receipt != nil {
				if *receipt != digest {
					return service.ErrConflict
				}
				return nil
			}
			if !finish {
				return tx.Q.StartCoresearchCell(r.Context(), db.StartCoresearchCellParams{WorkspaceID: tx.WS, ID: id})
			}
			outputs := json.RawMessage(in.Outputs)
			if blob != nil {
				outputs = nil
			}
			if _, e := tx.Q.FinishCoresearchCell(r.Context(), db.FinishCoresearchCellParams{WorkspaceID: tx.WS, ID: id, Status: in.Status, ExecutionCount: in.ExecutionCount, Outputs: outputs, OutputsBlob: blob, ReceiptSha: &digest}); e != nil {
				return e
			}
			return coresearch.Changed(r.Context(), tx, "cell", c, id)
		})
	}
	if finish {
		e = coresearch.WithCellOutput(r.Context(), h.Store, c.WorkspaceID, id, finishCell)
	} else {
		e = finishCell()
	}

	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
func (h *H) daemonCoresearchSetup(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "id")
	if !coresearchAnswer(w, r, e) {
		return
	}
	var in struct {
		Status        string `json:"status"`
		Log           string `json:"log"`
		PythonVersion string `json:"python_version"`
	}
	e = h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
		s, e := tx.Q.GetCoresearchSetup(r.Context(), db.GetCoresearchSetupParams{WorkspaceID: tx.WS, ID: id})
		if e != nil {
			return e
		}
		c, e := daemonCoresearchChat(r, tx, s.ChatID, d)
		if e != nil {
			return e
		}
		if s.ClaimedDaemonID == nil || *s.ClaimedDaemonID != d {
			return pgx.ErrNoRows
		}
		if e = coresearchClaim(r, s.ClaimToken, s.LeaseExpiresAt, s.Status, s.ReceiptSha, true); e != nil {
			return e
		}
		if readJSON(r, &in) != nil || !slices.Contains([]string{"running", "ok", "failed"}, in.Status) || len(in.Log) > 262144 || in.PythonVersion != "" && !service.ValidPythonVersion(in.PythonVersion) || in.Status != "failed" && in.PythonVersion == "" {
			return cardInputError("invalid setup result")
		}
		digest := coresearchReceipt(in)
		if s.ReceiptSha != nil {
			if in.Status == "running" || *s.ReceiptSha != digest {
				return service.ErrConflict
			}
			return nil
		}
		if in.PythonVersion != "" {
			p, e := tx.Q.GetCoresearchProject(r.Context(), db.GetCoresearchProjectParams{WorkspaceID: tx.WS, ID: c.ProjectID})
			if e != nil {
				return e
			}
			if p.PythonVersion != nil && *p.PythonVersion != in.PythonVersion {
				return conflict("project Python version is already reserved")
			}
			if p.PythonVersion == nil {
				if e = tx.Q.SetCoresearchPython(r.Context(), db.SetCoresearchPythonParams{WorkspaceID: tx.WS, ID: c.ProjectID, PythonVersion: &in.PythonVersion}); e != nil {
					return e
				}
			}
		}
		if in.Status != "running" {
			if _, e = tx.Q.FinishCoresearchSetup(r.Context(), db.FinishCoresearchSetupParams{WorkspaceID: tx.WS, ID: id, Status: in.Status, Log: in.Log, ReceiptSha: &digest}); e != nil {
				return e
			}
		}
		return coresearch.Changed(r.Context(), tx, "setup", c, id)
	})
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
func (h *H) daemonCoresearchKernel(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "chat")
	if !coresearchAnswer(w, r, e) {
		return
	}
	e = h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
		c, e := daemonCoresearchChat(r, tx, id, d)
		if e != nil {
			return e
		}
		var in struct {
			State   string `json:"state"`
			SeqDone int64  `json:"seq_done"`
		}
		if readJSON(r, &in) != nil || !slices.Contains([]string{"stopped", "idle", "busy"}, in.State) || in.SeqDone < 0 {
			return cardInputError("invalid kernel state")
		}
		if e = tx.Q.ReportCoresearchKernel(r.Context(), db.ReportCoresearchKernelParams{WorkspaceID: tx.WS, ID: id, State: in.State, SeqDone: in.SeqDone}); e != nil {
			return e
		}
		return coresearch.Changed(r.Context(), tx, "chat", c, id)
	})
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
func (h *H) daemonCoresearchStatus(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "id")
	if !coresearchAnswer(w, r, e) {
		return
	}
	var status string
	e = h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
		var chat uuid.UUID
		var claimed *string
		var token *uuid.UUID
		var expires *time.Time
		kind := chi.URLParam(r, "kind")
		switch kind {
		case "cells":
			row, e := tx.Q.GetCoresearchCell(r.Context(), db.GetCoresearchCellParams{WorkspaceID: tx.WS, ID: id})
			if e != nil {
				return e
			}
			chat, status, claimed = row.ChatID, row.Status, row.ClaimedDaemonID
			token, expires = row.ClaimToken, row.LeaseExpiresAt
		case "runs":
			row, e := tx.Q.GetCoresearchRun(r.Context(), db.GetCoresearchRunParams{WorkspaceID: tx.WS, ID: id})
			if e != nil {
				return e
			}
			chat, status, claimed = row.ChatID, row.Status, row.ClaimedDaemonID
			token, expires = row.ClaimToken, row.LeaseExpiresAt
		case "setups":
			row, e := tx.Q.GetCoresearchSetup(r.Context(), db.GetCoresearchSetupParams{WorkspaceID: tx.WS, ID: id})
			if e != nil {
				return e
			}
			chat, status, claimed = row.ChatID, row.Status, row.ClaimedDaemonID
			token, expires = row.ClaimToken, row.LeaseExpiresAt
		default:
			return pgx.ErrNoRows
		}
		if claimed == nil || *claimed != d {
			return pgx.ErrNoRows
		}
		if _, e := daemonCoresearchChat(r, tx, chat, d); e != nil {
			return e
		}
		supplied, err := uuid.Parse(r.URL.Query().Get("claim_token"))
		if err != nil || token == nil || supplied != *token {
			return service.ErrConflict
		}
		if r.Method == "POST" {
			if e := coresearchClaim(r, token, expires, status, nil, false); e != nil {
				return e
			}
			var n int64
			switch kind {
			case "cells":
				n, err = tx.Q.HeartbeatCoresearchCell(r.Context(), db.HeartbeatCoresearchCellParams{WorkspaceID: tx.WS, ID: id, ClaimToken: token})
			case "runs":
				n, err = tx.Q.HeartbeatCoresearchRun(r.Context(), db.HeartbeatCoresearchRunParams{WorkspaceID: tx.WS, ID: id, ClaimToken: token})
			case "setups":
				n, err = tx.Q.HeartbeatCoresearchSetup(r.Context(), db.HeartbeatCoresearchSetupParams{WorkspaceID: tx.WS, ID: id, ClaimToken: token})
			}
			if err != nil {
				return err
			}
			if n != 1 {
				return service.ErrConflict
			}
		}
		return nil
	})
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 200, map[string]string{"status": status})
	}
}
func (h *H) daemonCoresearchFiles(w http.ResponseWriter, r *http.Request) {
	out, e := h.daemonPaperFiles(r)
	if paperAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}

func (h *H) daemonCoresearchBlob(w http.ResponseWriter, r *http.Request) {
	var v db.CoresearchFileVersion
	e := h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
		var e error
		v, e = tx.Q.CoresearchBlobForOwner(r.Context(), db.CoresearchBlobForOwnerParams{WorkspaceID: tx.WS, OwnerID: user(r), DaemonID: &d, Sha256: chi.URLParam(r, "sha")})
		return e
	})
	if !coresearchAnswer(w, r, e) {
		return
	}
	rc, e := h.Blobs.Get(r.Context(), v.BlobKey)
	if e != nil {
		logoFail(w, r, e)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, rc)
}

// Chat monitoring lets a host interrupt persistent kernels and agent turns on revocation.
func (h *H) daemonCoresearchChatStatus(w http.ResponseWriter, r *http.Request) {
	e := h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
		id, e := coresearchID(r, "chat")
		if e != nil {
			return e
		}
		_, e = daemonCoresearchChat(r, tx, id, d)
		return e
	})
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 200, map[string]string{"status": "active"})
	}
}

func coresearchRunWithOutputs(ctx context.Context, tx *store.Tx, run db.CoresearchRun) (any, error) {
	rows, err := tx.Q.CoresearchRunOutputs(ctx, db.CoresearchRunOutputsParams{WorkspaceID: tx.WS, RunID: &run.ID})
	if err != nil {
		return nil, err
	}
	refs := make([]coresearch.FileRef, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, coresearch.FileRef{Path: row.Path, FileID: row.FileID, N: row.N, SHA256: row.Sha256, Size: row.Size})
	}
	return struct {
		db.CoresearchRun
		Outputs []coresearch.FileRef `json:"outputs"`
	}{run, refs}, nil
}
