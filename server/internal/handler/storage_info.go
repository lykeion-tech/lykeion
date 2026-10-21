package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/storageinfo"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func storageCacheKey(owner uuid.UUID, daemon string) string {
	sum := sha256.Sum256([]byte(daemon))
	return "lyk:storage:" + owner.String() + ":" + hex.EncodeToString(sum[:])
}

func (h *H) reportStorage(w http.ResponseWriter, r *http.Request) {
	var in storageinfo.Report
	if err := readJSON(r, &in); err != nil || in.WorkspaceID == uuid.Nil || in.RuntimeID == uuid.Nil || strings.TrimSpace(in.DaemonID) == "" || len(in.DaemonID) > 256 || in.Snapshot.Validate() != nil {
		writeErr(w, 400, "valid storage snapshot and reporting identity required")
		return
	}
	in.Snapshot.ReceivedAt = time.Now().UTC()
	data, err := json.Marshal(in.Snapshot)
	if err != nil {
		fail(w, r, err)
		return
	}
	err = h.Store.InWorkspace(r.Context(), in.WorkspaceID, user(r), func(tx *store.Tx) error {
		if _, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: in.WorkspaceID, UserID: user(r)}); err != nil {
			return err
		}
		rt, err := tx.Q.LockRuntime(r.Context(), db.LockRuntimeParams{WorkspaceID: in.WorkspaceID, ID: in.RuntimeID})
		if err != nil {
			return err
		}
		if rt.OwnerID != user(r) || rt.DaemonID != in.DaemonID {
			return pgx.ErrNoRows
		}
		if h.Limit.R == nil {
			return fmt.Errorf("storage cache unavailable")
		}
		return h.Limit.R.Set(r.Context(), storageCacheKey(user(r), in.DaemonID), data, 0).Err()
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *H) getStorage(w http.ResponseWriter, r *http.Request) {
	out := storageinfo.Response{Machines: []storageinfo.Machine{}}
	err := h.inWS(r, func(tx *store.Tx, ws, owner uuid.UUID) error {
		rows, err := tx.Q.ListRuntimes(r.Context(), ws)
		if err != nil {
			return err
		}
		byDaemon := map[string]int{}
		for _, rt := range rows {
			if rt.OwnerID != owner {
				continue
			}
			if i, ok := byDaemon[rt.DaemonID]; ok {
				m := &out.Machines[i]
				m.Online = m.Online || rt.Status == "online"
				if rt.LastSeenAt.After(m.LastSeenAt) {
					m.LastSeenAt = rt.LastSeenAt
				}
				continue
			}
			name := rt.DeviceName
			if rt.CustomName != nil && *rt.CustomName != "" {
				name = *rt.CustomName
			}
			m := storageinfo.Machine{DaemonID: rt.DaemonID, Name: name, Online: rt.Status == "online", LastSeenAt: rt.LastSeenAt}
			if h.Limit.R == nil {
				return fmt.Errorf("storage cache unavailable")
			}
			data, err := h.Limit.R.Get(r.Context(), storageCacheKey(owner, rt.DaemonID)).Bytes()
			if err != nil && !errors.Is(err, redis.Nil) {
				return err
			}
			if err == nil {
				var snapshot storageinfo.Snapshot
				if err = json.Unmarshal(data, &snapshot); err != nil {
					return err
				}
				m.Snapshot = &snapshot
			}
			byDaemon[rt.DaemonID] = len(out.Machines)
			out.Machines = append(out.Machines, m)
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
