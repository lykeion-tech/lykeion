package records

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/unicode/norm"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// MaxCallAnswer is how much of an answer a call record keeps in its body; the
// whole answer is the record's blob when it is longer.
const MaxCallAnswer = 256 << 10

// Cut is s cut to what a record body keeps, MaxCallAnswer bytes at a rune
// boundary, and whether anything was cut.
func Cut(s string) (string, bool) {
	if len(s) <= MaxCallAnswer {
		return s, false
	}
	n := MaxCallAnswer
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// CallBody builds a call record's body: the answer cut to what a body keeps,
// and the whole answer as rest when it was cut, for the record's blob. The
// body is truncated when it was cut here or upstream had already cut the
// answer (upstreamCut).
func CallBody(server, tool string, args any, answer string, upstreamCut bool) (map[string]any, string) {
	sum := sha256.Sum256([]byte(answer))
	rest := ""
	if kept, cut := Cut(answer); cut {
		answer, rest = kept, answer
	}
	return map[string]any{"server": server, "tool": tool, "args": args, "answer_sha256": hex.EncodeToString(sum[:]),
		"answer": answer, "truncated": upstreamCut || rest != "", "at": time.Now().UTC().Format(time.RFC3339)}, rest
}

// ExtractText reads an attached file's text: text-like files as they are, PDFs
// through their text layer. Binaries, text that is not UTF-8 and PDFs the
// reader cannot parse have no text; only reading r fails.
func ExtractText(name, contentType string, r io.Reader) (text string, ok bool, err error) {
	typ := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	isPDF := typ == "application/pdf" || strings.EqualFold(path.Ext(name), ".pdf")
	if !isPDF && !strings.HasPrefix(typ, "text/") && typ != "application/json" && !strings.HasSuffix(typ, "+json") {
		return "", false, nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", false, err
	}
	if !isPDF {
		if !utf8.Valid(b) {
			return "", false, nil
		}
		return string(b), true, nil
	}
	text, ok = pdfText(b)
	return text, ok, nil
}

// pdfText is a PDF's text layer. The reader panics on some malformed input,
// which counts as no text.
func pdfText(b []byte) (text string, ok bool) {
	defer func() {
		if recover() != nil {
			text, ok = "", false
		}
	}()
	doc, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", false
	}
	tr, err := doc.GetPlainText()
	if err != nil {
		return "", false
	}
	out, err := io.ReadAll(tr)
	if err != nil || !utf8.Valid(out) {
		return "", false
	}
	return string(out), true
}

// AttachmentServer is the server name an attached file's call record carries;
// only the server writes such records.
const AttachmentServer = "attachments"

// RecordAttachment records an attached file once as a source of its study: a
// call record by the server on the study's first column, round 0, under the
// study's next call key. A file with text answers with it and keeps the whole
// text as the record's blob, so quotes from it are checked; a binary answers
// with its name and sha256 only. The caller holds the card.
func RecordAttachment(ctx context.Context, tx *store.Tx, blobs storage.Storage, card db.Card, a db.Attachment, text string, hasText bool) (key string, err error) {
	answer := fmt.Sprintf("binary file %s, sha256 %s", a.Name, a.Sha256)
	if hasText {
		answer = text
	}
	body, _ := CallBody(AttachmentServer, "attach", map[string]any{"name": a.Name, "sha256": a.Sha256, "size_bytes": a.SizeBytes, "content_type": a.ContentType}, answer, false)
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	if key, err = NextCallKey(ctx, tx, card.ID); err != nil {
		return "", err
	}
	blobKey := ""
	if hasText {
		blobKey = TextBlobKey(card, text)
		if err := blobs.Put(ctx, blobKey, strings.NewReader(text), int64(len(text))); err != nil {
			return "", err
		}
		if err := tx.Emit(ctx, "blob.created", &card.ID, store.System,
			map[string]any{"blob_key": blobKey, "sha256": body["answer_sha256"], "size": int64(len(text))}); err != nil {
			return "", err
		}
	}
	rec, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: card.WorkspaceID, CardID: card.ID, BoardColumn: card.ScopeFrom,
		Round: 0, Kind: "call", Role: "server", Key: key, Body: b, BlobKey: blobKey})
	if err != nil {
		return "", err
	}
	return key, tx.Emit(ctx, "record.created", &card.ID, store.System, map[string]any{"record_id": rec.ID, "kind": "call", "key": key, "role": "server"})
}

// TextBlobKey is where a study's recorded text is kept: under the study, by
// the text's hash, as fetched sources are.
func TextBlobKey(card db.Card, text string) string {
	sum := sha256.Sum256([]byte(text))
	return card.WorkspaceID.String() + "/" + card.ID.String() + "/" + hex.EncodeToString(sum[:])
}

// maxDiskName keeps a file name within what file systems allow (255 bytes).
const maxDiskName = 240

// DiskNames gives each attached file a name to have in a run folder: no path
// separators or leading dots, the extension kept, and unique within the study
// even on file systems that ignore case and Unicode normalisation, with
// "name (2).ext" for a clash.
func DiskNames(names []string) []string {
	out := make([]string, len(names))
	taken := map[string]bool{}
	for i, name := range names {
		clean := strings.TrimLeft(strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, name), ".")
		if clean == "" {
			clean = "file"
		}
		ext := path.Ext(clean)
		if len(ext) > 32 {
			ext = ""
		}
		stem := strings.TrimSuffix(clean, ext)
		for n := 1; ; n++ {
			suffix := ""
			if n > 1 {
				suffix = fmt.Sprintf(" (%d)", n)
			}
			candidate := cutBytes(stem, maxDiskName-len(suffix)-len(ext)) + suffix + ext
			if k := strings.ToLower(norm.NFC.String(candidate)); !taken[k] {
				taken[k], out[i] = true, candidate
				break
			}
		}
	}
	return out
}

func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
