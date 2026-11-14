package coresearch

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const EnvironmentFingerprintVersion = "coresearch-env-v1"

const MaxCellOutputBytes = 20 << 20
const InlineCellOutputBytes = 1 << 20

type FileRef struct {
	DocVersion *int64    `json:"doc_version,omitempty"`
	Path       string    `json:"path"`
	FileID     uuid.UUID `json:"file_id"`
	N          int32     `json:"n"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
}
type WorkContext struct {
	ChatID        uuid.UUID `json:"chat_id"`
	ProjectID     uuid.UUID `json:"project_id"`
	WorkspaceID   uuid.UUID `json:"workspace_id"`
	PythonVersion *string   `json:"python_version"`
	ClaimToken    uuid.UUID `json:"claim_token,omitempty"`
}
type ClaimedCell struct {
	WorkContext
	ID     uuid.UUID `json:"id"`
	Kernel string    `json:"kernel"`
	Source string    `json:"source"`
}
type ClaimedRun struct {
	WorkContext
	ID                 uuid.UUID `json:"id"`
	Key                string    `json:"key"`
	Script             FileRef   `json:"script"`
	Inputs             []FileRef `json:"inputs"`
	Seed               int64     `json:"seed"`
	ExpectedEnvSHA     *string   `json:"expected_env_sha"`
	ExpectedEnvVersion string    `json:"expected_env_version,omitempty"`
}
type ClaimedSetup struct {
	WorkContext
	ID     uuid.UUID `json:"id"`
	Python *string   `json:"python"`
	Conda  []string  `json:"conda"`
}
type ClaimedControl struct {
	WorkContext
	Op  string `json:"op"`
	Seq int64  `json:"seq"`
}
type Work struct {
	Builds   []ClaimedBuild   `json:"builds"`
	Cells    []ClaimedCell    `json:"cells"`
	Runs     []ClaimedRun     `json:"runs"`
	Setups   []ClaimedSetup   `json:"setups"`
	Controls []ClaimedControl `json:"controls"`
}

func EmptyWork() Work {
	return Work{Builds: []ClaimedBuild{}, Cells: []ClaimedCell{}, Runs: []ClaimedRun{}, Setups: []ClaimedSetup{}, Controls: []ClaimedControl{}}
}
func OutputsMatch(a, b map[string]string) bool { return maps.Equal(a, b) }
func Changed(ctx context.Context, tx *store.Tx, kind string, c db.CoresearchChat, id uuid.UUID) error {
	p := map[string]any{"project_id": c.ProjectID, kind + "_id": id}
	if kind == "cell" {
		p["chat_id"] = c.ID
	}
	return tx.Emit(ctx, "coresearch."+kind+".changed", nil, store.System, p)
}
func Wake(ctx context.Context, tx *store.Tx, c db.CoresearchChat) error {
	return tx.Emit(ctx, "coresearch.work.queued", nil, store.System, map[string]any{"owner_id": c.OwnerID})
}
func EnqueueCell(ctx context.Context, tx *store.Tx, c db.CoresearchChat, who uuid.UUID, run *uuid.UUID, kernel, source string) (db.CoresearchCell, error) {
	var zero db.CoresearchCell
	if len(kernel) == 0 || utf8.RuneCountInString(kernel) > 128 || len(source) > 262144 || !utf8.ValidString(source) || !utf8.ValidString(kernel) {
		return zero, fmt.Errorf("invalid kernel or source")
	}
	var author *uuid.UUID
	if run == nil {
		author = &who
	}
	row, e := tx.Q.EnqueueCoresearchCell(ctx, db.EnqueueCoresearchCellParams{WorkspaceID: tx.WS, ChatID: c.ID, AuthorUserID: author, AgentRunID: run, Kernel: kernel, Source: source})
	if e != nil {
		return zero, e
	}
	if e = Changed(ctx, tx, "cell", c, row.ID); e != nil {
		return zero, e
	}
	return row, Wake(ctx, tx, c)
}
func PinnedFile(ctx context.Context, tx *store.Tx, project uuid.UUID, path string) (FileRef, error) {
	var zero FileRef
	clean, e := CleanPath(path)
	if e != nil {
		return zero, e
	}
	f, e := tx.Q.CoresearchFileByPath(ctx, db.CoresearchFileByPathParams{WorkspaceID: tx.WS, ProjectID: project, Path: clean})
	if errors.Is(e, pgx.ErrNoRows) {
		return zero, fmt.Errorf("%s is not a saved project file; save it first", path)
	}
	if e != nil {
		return zero, e
	}
	v, e := tx.Q.CoresearchFileHead(ctx, db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: f.ID})
	if e != nil {
		return zero, e
	}
	return FileRef{Path: f.Path, FileID: f.ID, N: v.N, SHA256: v.Sha256, Size: v.Size}, nil
}
func FileRefs(ctx context.Context, tx *store.Tx, project uuid.UUID) ([]FileRef, error) {
	out := []FileRef{}
	fs, e := tx.Q.ListCoresearchFiles(ctx, db.ListCoresearchFilesParams{WorkspaceID: tx.WS, ProjectID: project})
	if e != nil {
		return nil, e
	}
	for _, f := range fs {
		v, e := PinnedFile(ctx, tx, project, f.Path)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func EnqueueRun(ctx context.Context, tx *store.Tx, chat db.CoresearchChat, requestedBy uuid.UUID, agentRun *uuid.UUID, script string, inputs []string, rerunOf *db.CoresearchRun) (db.CoresearchRun, error) {
	var zero db.CoresearchRun
	p, e := tx.Q.LockCoresearchProject(ctx, db.LockCoresearchProjectParams{WorkspaceID: tx.WS, ID: chat.ProjectID})
	if e != nil {
		return zero, e
	}
	if p.ArchivedAt != nil {
		return zero, ErrArchived
	}
	in := db.EnqueueCoresearchRunParams{WorkspaceID: tx.WS, ProjectID: chat.ProjectID, ChatID: chat.ID, RequestedBy: requestedBy, AgentRunID: agentRun}
	if rerunOf != nil {
		if rerunOf.Status != "ok" || rerunOf.EnvSha == nil || *rerunOf.EnvSha == "" || rerunOf.ChatID != chat.ID || rerunOf.ProjectID != chat.ProjectID {
			return zero, errors.New("only a successful run in this chat with a recorded environment can be rerun")
		}
		var facts struct {
			Version string `json:"env_fingerprint_version"`
		}
		if json.Unmarshal(rerunOf.Facts, &facts) != nil || facts.Version != EnvironmentFingerprintVersion {
			return zero, errors.New("the original run environment fingerprint is incompatible; create a new recorded run")
		}
		in.ScriptFileID = rerunOf.ScriptFileID
		in.ScriptN = rerunOf.ScriptN
		in.Inputs = rerunOf.Inputs
		in.Seed = rerunOf.Seed
		in.RerunOf = &rerunOf.ID
		in.ExpectedEnvSha = rerunOf.EnvSha
	} else {
		f, e := PinnedFile(ctx, tx, chat.ProjectID, script)
		if e != nil {
			return zero, e
		}
		in.ScriptFileID = f.FileID
		in.ScriptN = f.N
		refs := []FileRef{}
		if len(inputs) > 500 {
			return zero, errors.New("at most 500 inputs")
		}
		for _, path := range inputs {
			f, e := PinnedFile(ctx, tx, chat.ProjectID, path)
			if e != nil {
				return zero, e
			}
			refs = append(refs, f)
		}
		in.Inputs, _ = json.Marshal(refs)
		var b [8]byte
		if _, e = rand.Read(b[:]); e != nil {
			return zero, e
		}
		in.Seed = int64(binary.BigEndian.Uint64(b[:]) & 0x7fffffffffffffff)
	}
	in.Key, e = tx.Q.NextCoresearchRunKey(ctx, db.NextCoresearchRunKeyParams{WorkspaceID: tx.WS, ProjectID: chat.ProjectID})
	if e != nil {
		return zero, e
	}
	out, e := tx.Q.EnqueueCoresearchRun(ctx, in)
	if e != nil {
		return zero, e
	}
	if e = Changed(ctx, tx, "run", chat, out.ID); e != nil {
		return zero, e
	}
	return out, Wake(ctx, tx, chat)
}

// Claim runs under the same workspace lock as turn claims and membership changes.
func Claim(ctx context.Context, tx *store.Tx, who uuid.UUID, daemon string, runtimes []uuid.UUID) (Work, error) {
	out := EmptyWork()
	if err := Expire(ctx, tx); err != nil {
		return out, err
	}
	builds, e := tx.Q.ClaimCoresearchBuilds(ctx, db.ClaimCoresearchBuildsParams{WorkspaceID: tx.WS, OwnerID: who, DaemonID: &daemon, RuntimeIds: runtimes})
	if e != nil {
		return out, e
	}
	for _, b := range builds {
		var refs []FileRef
		if e = json.Unmarshal(b.Sources, &refs); e != nil {
			return out, e
		}
		texts := []DocText{}
		for _, ref := range refs {
			if ref.DocVersion == nil {
				continue
			}
			f, e := tx.Q.GetCoresearchRetainedFile(ctx, db.GetCoresearchRetainedFileParams{WorkspaceID: tx.WS, ProjectID: b.ProjectID, ID: ref.FileID})
			if e != nil {
				return out, e
			}
			d, e := doc.AtVersion(ctx, tx, doc.File{Record: f}, *ref.DocVersion)
			if e != nil {
				return out, e
			}
			texts = append(texts, DocText{Path: ref.Path, FileID: ref.FileID, Version: d.Version, Text: d.Text})
		}
		out.Builds = append(out.Builds, ClaimedBuild{WorkContext: WorkContext{WorkspaceID: tx.WS, ProjectID: b.ProjectID, ClaimToken: *b.ClaimToken}, ID: b.ID, Seq: b.Seq, Sources: refs, Docs: texts})
		if e = BuildChanged(ctx, tx, b); e != nil {
			return out, e
		}
	}
	chats, e := tx.Q.CoresearchClaimChats(ctx, db.CoresearchClaimChatsParams{WorkspaceID: tx.WS, OwnerID: who, DaemonID: &daemon, RuntimeIds: runtimes})
	if e != nil {
		return out, e
	}
	for _, c := range chats {
		p, e := tx.Q.GetCoresearchProject(ctx, db.GetCoresearchProjectParams{WorkspaceID: tx.WS, ID: c.ProjectID})
		if e != nil {
			return out, e
		}
		wc := WorkContext{ChatID: c.ID, ProjectID: c.ProjectID, WorkspaceID: tx.WS, PythonVersion: p.PythonVersion}
		claimed := false
		cell, e := tx.Q.ClaimCoresearchCell(ctx, db.ClaimCoresearchCellParams{WorkspaceID: tx.WS, ChatID: c.ID, DaemonID: &daemon})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return out, e
		}
		if e == nil {
			wc.ClaimToken = *cell.ClaimToken
			out.Cells = append(out.Cells, ClaimedCell{wc, cell.ID, cell.Kernel, cell.Source})
			claimed = true
			if e = Changed(ctx, tx, "cell", c, cell.ID); e != nil {
				return out, e
			}
		}
		runs, e := tx.Q.ClaimCoresearchRuns(ctx, db.ClaimCoresearchRunsParams{WorkspaceID: tx.WS, ChatID: c.ID, DaemonID: &daemon})
		if e != nil {
			return out, e
		}
		for _, r := range runs {
			f, e := tx.Q.GetCoresearchFile(ctx, db.GetCoresearchFileParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, ID: r.ScriptFileID})
			if e != nil {
				return out, e
			}
			v, e := tx.Q.CoresearchFileVersion(ctx, db.CoresearchFileVersionParams{WorkspaceID: tx.WS, FileID: f.ID, N: r.ScriptN})
			if e != nil {
				return out, e
			}
			var refs []FileRef
			if e = json.Unmarshal(r.Inputs, &refs); e != nil {
				return out, e
			}
			version := ""
			if r.RerunOf != nil {
				original, err := tx.Q.GetCoresearchRun(ctx, db.GetCoresearchRunParams{WorkspaceID: tx.WS, ID: *r.RerunOf})
				if err != nil {
					return out, err
				}
				var facts struct {
					Version string `json:"env_fingerprint_version"`
				}
				_ = json.Unmarshal(original.Facts, &facts)
				version = facts.Version
			}
			wc.ClaimToken = *r.ClaimToken
			out.Runs = append(out.Runs, ClaimedRun{wc, r.ID, r.Key, FileRef{Path: f.Path, FileID: f.ID, N: v.N, SHA256: v.Sha256, Size: v.Size}, refs, r.Seed, r.ExpectedEnvSha, version})
			claimed = true
			if e = Changed(ctx, tx, "run", c, r.ID); e != nil {
				return out, e
			}
		}
		setups, e := tx.Q.ClaimCoresearchSetups(ctx, db.ClaimCoresearchSetupsParams{WorkspaceID: tx.WS, ChatID: c.ID, DaemonID: &daemon})
		if e != nil {
			return out, e
		}
		for _, s := range setups {
			wc.ClaimToken = *s.ClaimToken
			out.Setups = append(out.Setups, ClaimedSetup{wc, s.ID, s.Python, s.Conda})
			claimed = true
			if e = Changed(ctx, tx, "setup", c, s.ID); e != nil {
				return out, e
			}
		}
		if c.KernelRequest != nil {
			wc.ClaimToken = uuid.Nil
			out.Controls = append(out.Controls, ClaimedControl{wc, *c.KernelRequest, c.KernelRequestSeq})
			claimed = true
		}
		if claimed && c.DaemonID == nil {
			if e = tx.Q.BindCoresearchChat(ctx, db.BindCoresearchChatParams{WorkspaceID: tx.WS, ID: c.ID, DaemonID: &daemon}); e != nil {
				return out, e
			}
			if e = Changed(ctx, tx, "chat", c, c.ID); e != nil {
				return out, e
			}
		}
	}
	return out, nil
}
