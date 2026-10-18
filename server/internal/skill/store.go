package skill

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/science"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Service struct {
	Store *store.Store
	Blobs storage.Storage
}
type Source struct {
	Kind      string `json:"kind"`
	Location  string `json:"location"`
	RuntimeID string `json:"runtime_id,omitempty"`
}
type Scope struct {
	Kind string
	ID   uuid.UUID
}
type Summary struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Domain          string     `json:"domain"`
	Origin          string     `json:"origin"`
	Revision        int64      `json:"revision"`
	CreatedBy       *uuid.UUID `json:"created_by"`
	Source          Source     `json:"source"`
	ContainsScripts bool       `json:"contains_scripts"`
	Editable        bool       `json:"editable"`
	Licence         string     `json:"licence,omitempty"`
}
type Detail struct {
	Summary
	Content string     `json:"content"`
	Files   []FileMeta `json:"files"`
}

var (
	ErrForbidden = errors.New("skill access is forbidden")
	ErrConflict  = errors.New("a skill with this name already exists; rename the skill")
	ErrInvalid   = errors.New("invalid skill")
)

type StaleRevision struct{ Revision int64 }

func (e *StaleRevision) Error() string { return "skill changed; reload its saved version" }
func invalid(err error) error          { return fmt.Errorf("%w: %v", ErrInvalid, err) }
func conflict(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return ErrConflict
	}
	return err
}
func member(ctx context.Context, tx *store.Tx, user uuid.UUID, write bool) (db.Member, error) {
	if write {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
			return db.Member{}, err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, tx.WS); err != nil {
			return db.Member{}, err
		}
	}
	m, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return m, pgx.ErrNoRows
	}
	return m, err
}
func canEdit(role string, creator, user uuid.UUID) bool {
	return creator == user || role == "owner" || role == "admin"
}
func bundledSummary(sk science.Skill) Summary {
	return Summary{ID: "bundle:" + sk.Name, Name: sk.Name, Description: sk.Description, Domain: sk.Domain, Origin: "bundled", Revision: 1, Source: Source{Kind: "bundled"}, Licence: sk.Licence}
}
func summary(id uuid.UUID, name, description, domain string, revision int64, creator, user uuid.UUID, role string, source Source, scripts bool) Summary {
	return Summary{ID: id.String(), Name: name, Description: description, Domain: domain, Origin: "imported", Revision: revision, CreatedBy: &creator, Source: source, ContainsScripts: scripts, Editable: canEdit(role, creator, user)}
}
func sourceOf(kind, location string, runtime *uuid.UUID) Source {
	s := Source{Kind: kind, Location: location}
	if runtime != nil {
		s.RuntimeID = runtime.String()
	}
	return s
}

type manifest struct {
	Files           []FileMeta `json:"files"`
	ContainsScripts bool       `json:"contains_scripts"`
}

func readManifest(raw []byte) (manifest, error) {
	var m manifest
	err := json.Unmarshal(raw, &m)
	return m, err
}

func (s *Service) List(ctx context.Context, ws, user uuid.UUID) ([]Summary, error) {
	out := []Summary{}
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		m, err := member(ctx, tx, user, false)
		if err != nil {
			return err
		}
		for _, sk := range science.Skills() {
			v := bundledSummary(sk)
			b, err := bundled(sk.Name)
			if err != nil {
				return err
			}
			v.ContainsScripts = containsScripts(b)
			out = append(out, v)
		}
		rows, err := tx.Q.ListWorkspaceSkills(ctx, ws)
		if err != nil {
			return err
		}
		for _, r := range rows {
			saved, err := readManifest(r.Manifest)
			if err != nil {
				return err
			}
			out = append(out, summary(r.ID, r.Name, r.Description, r.Domain, r.Revision, r.CreatedBy, user, m.Role, sourceOf(r.SourceKind, r.SourceLocation, r.SourceRuntimeID), saved.ContainsScripts))
		}
		return nil
	})
	return out, err
}
func bundled(name string) (Bundle, error) {
	var out bytes.Buffer
	if err := science.Tar(&out, name); err != nil {
		return Bundle{}, pgx.ErrNoRows
	}
	return readTar(out.Bytes())
}
func readTar(data []byte) (Bundle, error) {
	b := Bundle{Files: map[string][]byte{}}
	r := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return b, err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg || !validPath(h.Name) || h.Size > FileBytes || len(b.Files) > SupportingFiles {
			return b, fmt.Errorf("invalid skill archive")
		}
		if _, ok := b.Files[h.Name]; ok {
			return b, fmt.Errorf("duplicate skill archive path")
		}
		raw, err := io.ReadAll(io.LimitReader(r, FileBytes+1))
		if err != nil {
			return b, err
		}
		b.Files[h.Name] = raw
	}
	return b, nil
}

type byteCounter struct{ size int64 }

func (c *byteCounter) Write(p []byte) (int, error) { c.size += int64(len(p)); return len(p), nil }

// storedArchiveSize includes the encoder's path metadata, padding and footer.
// Payload limits apply to files; the ZIP upload limit does not bound a tar.
func storedArchiveSize(files []FileMeta) (int64, error) {
	if len(files) == 0 || len(files) > SupportingFiles+1 {
		return 0, fmt.Errorf("invalid skill manifest file count")
	}
	seen := map[string]bool{}
	supporting, count := int64(0), 0
	main := false
	var out byteCounter
	w := tar.NewWriter(&out)
	zeros := make([]byte, FileBytes)
	for _, f := range files {
		if !validPath(f.Path) || seen[f.Path] || f.Size < 0 || f.Size > FileBytes {
			return 0, fmt.Errorf("invalid skill manifest file")
		}
		seen[f.Path] = true
		if f.Path == "SKILL.md" {
			main = true
		} else {
			supporting += f.Size
			count++
			if strings.EqualFold(path.Base(f.Path), "SKILL.md") || supporting > SupportingBytes || count > SupportingFiles {
				return 0, fmt.Errorf("invalid skill manifest supporting files")
			}
		}
		if err := w.WriteHeader(tarHeader(f.Path, f.Size)); err != nil {
			return 0, err
		}
		if _, err := w.Write(zeros[:f.Size]); err != nil {
			return 0, err
		}
	}
	if !main {
		return 0, fmt.Errorf("skill manifest has no SKILL.md")
	}
	for p := range seen {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return 0, fmt.Errorf("skill manifest file is also used as a directory")
			}
		}
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return out.size, nil
}

func (s *Service) load(ctx context.Context, row db.GetWorkspaceSkillRow) (Bundle, error) {
	if row.BlobKey != row.WorkspaceID.String()+"/skills/"+row.Sha256 {
		return Bundle{}, fmt.Errorf("invalid skill blob identity")
	}
	saved, err := readManifest(row.Manifest)
	if err != nil {
		return Bundle{}, err
	}
	size, err := storedArchiveSize(saved.Files)
	if err != nil {
		return Bundle{}, err
	}
	r, err := s.Blobs.Get(ctx, row.BlobKey)
	if err != nil {
		return Bundle{}, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return Bundle{}, err
	}
	if int64(len(data)) != size || hash(data) != row.Sha256 {
		return Bundle{}, fmt.Errorf("skill blob hash mismatch")
	}
	b, err := readTar(data)
	if err != nil {
		return b, err
	}
	b.Name = row.Name
	b.Description = row.Description
	return b, Validate(b)
}
func (s *Service) Get(ctx context.Context, ws, user uuid.UUID, id string) (Detail, error) {
	var out Detail
	var row db.GetWorkspaceSkillRow
	var role string
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		m, err := member(ctx, tx, user, false)
		if err != nil {
			return err
		}
		role = m.Role
		if strings.HasPrefix(id, "bundle:") {
			if !science.Has(strings.TrimPrefix(id, "bundle:")) {
				return pgx.ErrNoRows
			}
			return nil
		}
		uid, err := uuid.Parse(id)
		if err != nil {
			return pgx.ErrNoRows
		}
		row, err = tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: uid})
		return err
	})
	if err != nil {
		return out, err
	}
	var b Bundle
	if name, ok := strings.CutPrefix(id, "bundle:"); ok {
		b, err = bundled(name)
		for _, sk := range science.Skills() {
			if sk.Name == name {
				out.Summary = bundledSummary(sk)
				break
			}
		}
	} else {
		b, err = s.load(ctx, row)
		out.Summary = summary(row.ID, row.Name, row.Description, row.Domain, row.Revision, row.CreatedBy, user, role, sourceOf(row.SourceKind, row.SourceLocation, row.SourceRuntimeID), false)
	}
	if err != nil {
		return out, err
	}
	out.Content = string(b.Files["SKILL.md"])
	out.Files = fileMetadata(b)
	out.ContainsScripts = containsScripts(b)
	// Fence membership and the active revision after the external blob read.
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		m, err := member(ctx, tx, user, false)
		if err != nil {
			return err
		}
		if out.Origin == "bundled" {
			return nil
		}
		current, err := tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: row.ID})
		if err != nil {
			return err
		}
		if current.Revision != row.Revision {
			return &StaleRevision{Revision: current.Revision}
		}
		out.Editable = canEdit(m.Role, current.CreatedBy, user)
		return nil
	})
	return out, err
}
func (s *Service) File(ctx context.Context, ws, user uuid.UUID, id, p string) ([]byte, error) {
	if !validPath(p) {
		return nil, invalid(fmt.Errorf("unsafe file path"))
	}
	d, err := s.Get(ctx, ws, user, id)
	if err != nil {
		return nil, err
	}
	if d.Origin == "bundled" {
		for _, f := range d.Files {
			if f.Path == p {
				return science.File(d.Name + "/" + p)
			}
		}
		return nil, pgx.ErrNoRows
	}
	var row db.GetWorkspaceSkillRow
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := member(ctx, tx, user, false); err != nil {
			return err
		}
		var err error
		row, err = tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: uuid.MustParse(id)})
		return err
	})
	if err != nil {
		return nil, err
	}
	b, err := s.load(ctx, row)
	if err != nil {
		return nil, err
	}
	data, ok := b.Files[p]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := member(ctx, tx, user, false); err != nil {
			return err
		}
		v, err := tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: row.ID})
		if err != nil {
			return err
		}
		if v.Revision != row.Revision {
			return &StaleRevision{Revision: v.Revision}
		}
		return nil
	})
	return data, err
}
func (s *Service) put(ctx context.Context, ws uuid.UUID, b Bundle) (db.InsertWorkspaceSkillVersionParams, error) {
	data, err := Tar(b)
	if err != nil {
		return db.InsertWorkspaceSkillVersionParams{}, invalid(err)
	}
	sha := hash(data)
	key := ws.String() + "/skills/" + sha
	if err := s.Blobs.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
		return db.InsertWorkspaceSkillVersionParams{}, err
	}
	rawManifest, err := json.Marshal(manifest{Files: fileMetadata(b), ContainsScripts: containsScripts(b)})
	return db.InsertWorkspaceSkillVersionParams{WorkspaceID: ws, Name: b.Name, Description: b.Description, Sha256: sha, BlobKey: key, Manifest: rawManifest}, err
}
func (s *Service) Create(ctx context.Context, ws, user uuid.UUID, b Bundle, source Source) (Detail, error) {
	return s.CreateFenced(ctx, ws, user, b, source, nil, nil)
}

// CreateFenced prepares blob bytes before entering the publication transaction.
// The callbacks may only use the supplied transaction; they fence the operation
// and commit its receipt alongside the skill and its event.
func (s *Service) CreateFenced(ctx context.Context, ws, user uuid.UUID, b Bundle, source Source, before func(*store.Tx) error, after func(*store.Tx, Detail) error) (Detail, error) {
	var out Detail
	var err error
	if err = Validate(b); err != nil {
		return out, invalid(err)
	}
	b, err = metadata(b)
	if err != nil {
		return out, invalid(err)
	}
	if science.Has(b.Name) {
		return out, ErrConflict
	}
	var runtime *uuid.UUID
	if source.RuntimeID != "" {
		v, e := uuid.Parse(source.RuntimeID)
		if e != nil {
			return out, invalid(e)
		}
		runtime = &v
	}
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error { _, err := member(ctx, tx, user, false); return err })
	if err != nil {
		return out, err
	}
	v, err := s.put(ctx, ws, b)
	if err != nil {
		return out, err
	}
	v.SkillID = uuid.New()
	v.Revision = 1
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		m, err := member(ctx, tx, user, true)
		if err != nil {
			return err
		}
		if before != nil {
			if err := before(tx); err != nil {
				return err
			}
		}
		row, err := tx.Q.InsertWorkspaceSkill(ctx, db.InsertWorkspaceSkillParams{WorkspaceID: ws, ID: v.SkillID, Name: b.Name, CreatedBy: user, SourceKind: source.Kind, SourceLocation: source.Location, SourceRuntimeID: runtime})
		if err != nil {
			return conflict(err)
		}
		if _, err = tx.Q.InsertWorkspaceSkillVersion(ctx, v); err != nil {
			return err
		}
		out = Detail{Summary: summary(row.ID, b.Name, b.Description, "Workspace", 1, user, user, m.Role, source, containsScripts(b)), Content: string(b.Files["SKILL.md"]), Files: fileMetadata(b)}
		if err := tx.Emit(ctx, "skill.created", nil, store.User(user), map[string]any{"skill_id": row.ID, "name": b.Name, "revision": 1}); err != nil {
			return err
		}
		if after != nil {
			return after(tx, out)
		}
		return nil
	})
	return out, err
}
func (s *Service) writable(ctx context.Context, ws, user uuid.UUID, id string, expected int64) (Detail, error) {
	var out Detail
	if strings.HasPrefix(id, "bundle:") {
		return out, ErrForbidden
	}
	if expected < 1 {
		return out, invalid(fmt.Errorf("revision must be positive"))
	}
	out, err := s.Get(ctx, ws, user, id)
	if err != nil {
		return out, err
	}
	if !out.Editable {
		return out, ErrForbidden
	}
	if out.Revision != expected {
		return out, &StaleRevision{Revision: out.Revision}
	}
	return out, nil
}
func (s *Service) Update(ctx context.Context, ws, user uuid.UUID, id string, expected int64, content string) (Detail, error) {
	before, err := s.writable(ctx, ws, user, id, expected)
	if err != nil {
		return Detail{}, err
	}
	var row db.GetWorkspaceSkillRow
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		var e error
		row, e = tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: uuid.MustParse(id)})
		return e
	})
	if err != nil {
		return Detail{}, err
	}
	if row.Revision != expected {
		return Detail{}, &StaleRevision{Revision: row.Revision}
	}
	b, err := s.load(ctx, row)
	if err != nil {
		return Detail{}, err
	}
	b.Files["SKILL.md"] = []byte(content)
	if err = Validate(b); err != nil {
		return Detail{}, invalid(err)
	}
	b, err = metadata(b)
	if err != nil {
		return Detail{}, invalid(err)
	}
	if science.Has(b.Name) {
		return Detail{}, ErrConflict
	}
	v, err := s.put(ctx, ws, b)
	if err != nil {
		return Detail{}, err
	}
	v.SkillID = row.ID
	v.Revision = expected + 1
	var out Detail
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		m, err := member(ctx, tx, user, true)
		if err != nil {
			return err
		}
		locked, err := tx.Q.LockWorkspaceSkill(ctx, db.LockWorkspaceSkillParams{WorkspaceID: ws, ID: row.ID})
		if err != nil {
			return err
		}
		if locked.DeletedAt != nil {
			return pgx.ErrNoRows
		}
		if !canEdit(m.Role, locked.CreatedBy, user) {
			return ErrForbidden
		}
		if locked.Revision != expected {
			return &StaleRevision{Revision: locked.Revision}
		}
		saved, err := tx.Q.UpdateWorkspaceSkill(ctx, db.UpdateWorkspaceSkillParams{WorkspaceID: ws, ID: row.ID, Name: b.Name, ExpectedRevision: expected})
		if err != nil {
			return conflict(err)
		}
		if _, err = tx.Q.InsertWorkspaceSkillVersion(ctx, v); err != nil {
			return err
		}
		out = Detail{Summary: summary(saved.ID, b.Name, b.Description, "Workspace", saved.Revision, saved.CreatedBy, user, m.Role, before.Source, containsScripts(b)), Content: content, Files: fileMetadata(b)}
		return tx.Emit(ctx, "skill.updated", nil, store.User(user), map[string]any{"skill_id": saved.ID, "name": saved.Name, "revision": saved.Revision})
	})
	return out, err
}
func (s *Service) Delete(ctx context.Context, ws, user uuid.UUID, id string, expected int64) error {
	if _, err := s.writable(ctx, ws, user, id, expected); err != nil {
		return err
	}
	return s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		m, err := member(ctx, tx, user, true)
		if err != nil {
			return err
		}
		uid := uuid.MustParse(id)
		row, err := tx.Q.LockWorkspaceSkill(ctx, db.LockWorkspaceSkillParams{WorkspaceID: ws, ID: uid})
		if err != nil {
			return err
		}
		if row.DeletedAt != nil {
			return pgx.ErrNoRows
		}
		if !canEdit(m.Role, row.CreatedBy, user) {
			return ErrForbidden
		}
		if row.Revision != expected {
			return &StaleRevision{Revision: row.Revision}
		}
		if _, err = tx.Q.DeleteWorkspaceSkill(ctx, db.DeleteWorkspaceSkillParams{WorkspaceID: ws, ID: uid, ExpectedRevision: expected}); err != nil {
			return err
		}
		return tx.Emit(ctx, "skill.deleted", nil, store.User(user), map[string]any{"skill_id": uid, "name": row.Name, "revision": row.Revision})
	})
}
