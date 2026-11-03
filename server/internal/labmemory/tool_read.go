package labmemory

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Pages bound both tool response size and server memory. The next byte offset
// always falls at a UTF-8 boundary. Binary files have metadata but no text.
const pageSize = 128 << 10

type blobPage struct {
	Section    string `json:"section"`
	Text       string `json:"text,omitempty"`
	NextOffset *int64 `json:"next_offset,omitempty"`
	Note       string `json:"note,omitempty"`
}

func digest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}

func blobSections(ctx context.Context, tx *store.Tx, f Fact) (map[string]string, error) {
	if f.Ref.Kind != "record" {
		return nil, nil
	}
	var r db.Record
	if err := json.Unmarshal([]byte(f.Text), &r); err != nil {
		return nil, err
	}
	// Collect only pointers written by record producers, and only from this
	// record's card. Attachment text additionally requires a matching live file.
	keys := map[string]string{}
	// Member output downloads resolve the current live "output" under the
	// record key. Older outputs and rerun artifacts still have visible metadata.
	if r.Kind == "rerun_output" {
		return keys, nil
	}
	if r.Kind == "output" {
		live, err := tx.Q.LiveCardRecordByKey(ctx, db.LiveCardRecordByKeyParams{WorkspaceID: tx.WS, CardID: r.CardID, Key: r.Key})
		if errors.Is(err, pgx.ErrNoRows) {
			return keys, nil
		}
		if err != nil {
			return nil, err
		}
		if live.ID != r.ID || live.Kind != "output" {
			return keys, nil
		}
	}

	if r.BlobKey != "" {
		keys["body"] = r.BlobKey
	}
	prefix := tx.WS.String() + "/" + r.CardID.String() + "/"
	if r.Kind == "run" {
		var body struct {
			ErrorBlob string `json:"error_blob"`
			Packages  string `json:"packages_sha256"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, err
		}
		if body.ErrorBlob != "" {
			keys["error"] = body.ErrorBlob
		}
		if digest(body.Packages) {
			keys["packages"] = prefix + body.Packages
		}
	}
	for section, key := range keys {
		valid := strings.HasPrefix(key, prefix) && digest(strings.TrimPrefix(key, prefix))
		if !valid && section == "body" && r.Kind == "output" {
			s, ok := strings.CutPrefix(key, tx.WS.String()+"/outputs/")
			id, e := uuid.Parse(s)
			valid = ok && e == nil && id.String() == s
		}
		if !valid && section == "body" && r.Kind == "call" && r.Role == "server" {
			s, ok := strings.CutPrefix(key, tx.WS.String()+"/attachments/")
			s, suffix := strings.CutSuffix(s, ".text")
			id, e := uuid.Parse(s)
			if ok && suffix && e == nil && id.String() == s {
				a, e := tx.Q.CardAttachment(ctx, db.CardAttachmentParams{WorkspaceID: tx.WS, CardID: &r.CardID, ID: id})
				valid = e == nil && a.Kind == "file" && a.RecordKey == r.Key && a.BlobKey+".text" == key && a.TextSha256 != ""
			}
		}
		if !valid {
			return nil, ErrNotFound
		}
		ok, err := tx.Q.RoleBlobExists(ctx, db.RoleBlobExistsParams{WorkspaceID: tx.WS, CardID: &r.CardID, BlobKey: key})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNotFound
		}
	}
	return keys, nil
}

func (c toolCall) read(ctx context.Context, in readInput) (evidence, error) {
	var out evidence
	var key string
	if in.Section == "" {
		in.Section = "body"
	}
	resolve := func(tx *store.Tx, asker uuid.UUID) (evidence, string, error) {
		ref, err := ParseRef(in.Ref)
		if err != nil {
			return evidence{}, "", ErrNotFound
		}
		f, err := Read(ctx, tx, asker, ref)
		if err != nil {
			return evidence{}, "", err
		}
		keys, err := blobSections(ctx, tx, f)
		if err != nil {
			return evidence{}, "", err
		}
		key := keys[in.Section]
		if in.Offset < 0 || key == "" && (in.Offset != 0 || in.Section != "body") {
			return evidence{}, "", errOffset
		}
		e := evidence{historyPin: pin(f).Version, Ref: ref.String(), Title: f.Title, Text: f.Text, Version: f.Version, Superseded: f.Superseded, Links: []string{}}
		for _, link := range f.Links {
			e.Links = append(e.Links, link.String())
		}
		for _, name := range []string{"body", "error", "packages"} {
			if keys[name] != "" {
				e.Sections = append(e.Sections, name)
			}
		}
		return e, key, nil
	}
	err := c.authorized(ctx, func(tx *store.Tx, asker uuid.UUID) error {
		var e error
		out, key, e = resolve(tx, asker)
		if e == nil && key == "" {
			ref, _ := ParseRef(out.Ref)
			e = c.log(ctx, tx, ref, "read", out.historyPin)
		}
		return e
	})
	if err != nil || key == "" {
		return out, err
	}
	// Never hold lifecycle/membership locks while waiting on blob storage.
	page, err := readPage(ctx, c.Blobs, key, in.Section, in.Offset)
	if err != nil {
		return evidence{}, err
	}
	err = c.authorized(ctx, func(tx *store.Tx, asker uuid.UUID) error {
		fresh, k, e := resolve(tx, asker)
		if e != nil {
			return e
		}
		if k != key || fresh.Version != out.Version {
			return ErrNotFound
		}
		out = fresh
		out.Blob = &page
		ref, _ := ParseRef(out.Ref)
		return c.log(ctx, tx, ref, "read", out.historyPin)
	})
	return out, err
}

func readPage(ctx context.Context, blobs storage.Storage, key, section string, offset int64) (blobPage, error) {
	p := blobPage{Section: section}
	if blobs == nil {
		p.Note = "Full text is unavailable."
		return p, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, err := blobs.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		p.Note = "Full text is unavailable."
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer r.Close()
	if offset > 0 {
		if _, err = io.CopyN(io.Discard, r, offset); err != nil {
			return p, errOffset
		}
	}
	b, err := io.ReadAll(io.LimitReader(r, pageSize+1))
	if err != nil {
		return p, err
	}
	if len(b) > pageSize {
		n := pageSize
		for n > pageSize-utf8.UTFMax && !utf8.RuneStart(b[n]) {
			n--
		}
		if !utf8.RuneStart(b[n]) {
			p.Note = "This record's blob is not readable text."
			return p, nil
		}
		b = b[:n]
		next := offset + int64(n)
		p.NextOffset = &next
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		p.NextOffset = nil
		p.Note = "This record's blob is not readable text."
		return p, nil
	}
	p.Text = string(b)
	return p, nil
}
