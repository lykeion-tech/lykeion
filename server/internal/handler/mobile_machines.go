package handler

import (
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type mobileRuntime struct {
	ID         uuid.UUID `json:"id"`
	Harness    string    `json:"harness"`
	Status     string    `json:"status"`
	Problem    *string   `json:"problem"`
	Active     int32     `json:"active_runs"`
	Version    string    `json:"cli_version"`
	Sharing    string    `json:"sharing,omitempty"`
	Colleagues []string  `json:"colleagues,omitempty"`
}
type mobileMachine struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Owner    string          `json:"owner"`
	Owned    bool            `json:"owned"`
	Online   bool            `json:"online"`
	Runtimes []mobileRuntime `json:"runtimes"`
}

func (h *H) mobileMachines(w http.ResponseWriter, r *http.Request) {
	machines := []*mobileMachine{}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		rows, err := tx.Q.ListRuntimes(r.Context(), ws)
		if err != nil || len(rows) == 0 {
			return err
		}
		members, err := tx.Q.ListMembers(r.Context(), ws)
		if err != nil {
			return err
		}
		names := map[uuid.UUID]string{}
		for _, member := range members {
			name := member.Name
			if name == "" {
				name = member.Email
			}
			names[member.UserID] = name
		}
		byID := map[string]*mobileMachine{}
		for _, rt := range rows {
			key := rt.OwnerID.String() + "/" + rt.DaemonID
			m := byID[key]
			if m == nil {
				m = &mobileMachine{ID: key, Name: rt.DeviceName, Owner: names[rt.OwnerID], Owned: rt.OwnerID == who}
				byID[key] = m
				machines = append(machines, m)
			}
			if rt.CustomName != nil {
				m.Name = *rt.CustomName
			}
			m.Online = m.Online || rt.Status == "online"
			runtime := mobileRuntime{ID: rt.ID, Harness: rt.Harness, Status: rt.Status, Problem: rt.UnhealthyReason, Active: rt.ActiveRuns, Version: rt.CliVersion}
			if m.Owned {
				runtime.Sharing = rt.Visibility
				for _, id := range rt.SharedWith {
					if name := names[id]; name != "" {
						runtime.Colleagues = append(runtime.Colleagues, name)
					}
				}
				sort.Strings(runtime.Colleagues)
			}
			m.Runtimes = append(m.Runtimes, runtime)
		}
		for _, m := range machines {
			sort.Slice(m.Runtimes, func(i, j int) bool { return m.Runtimes[i].Harness < m.Runtimes[j].Harness })
		}
		sort.SliceStable(machines, func(i, j int) bool {
			if machines[i].Online != machines[j].Online {
				return machines[i].Online
			}
			return machines[i].Name < machines[j].Name
		})
		return nil
	})
	if answer(w, r, err) {
		writeJSON(w, 200, machines)
	}
}

func (h *H) renameMobileMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name *string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == nil {
		writeErr(w, 400, "name required")
		return
	}
	name := strings.TrimSpace(*in.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		writeErr(w, 400, "Use a machine name between 1 and 64 characters.")
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		ids, err := tx.Q.RenameOwnedMachine(r.Context(), db.RenameOwnedMachineParams{WorkspaceID: ws, OwnerID: who, RuntimeID: id, CustomName: &name})
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return pgx.ErrNoRows
		}
		for _, id := range ids {
			if err := tx.Emit(r.Context(), "runtime.updated", nil, store.User(who), map[string]any{"runtime_id": id, "custom_name": name}); err != nil {
				return err
			}
		}
		return nil
	})
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}
