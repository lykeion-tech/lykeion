// Package labindex stores discovery vectors, never source text. Candidates must
// always be read live with the asker's authorization before use.
package labindex

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"lykeion/server/internal/embed"
	"lykeion/server/internal/labmemory"
	"math"
	"strings"
)

var ErrUnavailable = errors.New("Redis Search unavailable")

type Index struct {
	rdb   *redis.Client
	e     embed.Embedder
	model string
}

func New(rdb *redis.Client, e embed.Embedder, model string) *Index { return &Index{rdb, e, model} }
func (i *Index) Ensure(ctx context.Context) error {
	err := i.rdb.Do(ctx, "FT.CREATE", "idx:lab", "ON", "HASH", "PREFIX", 1, "lab:", "SCHEMA", "workspace", "TAG", "kind", "TAG", "model", "TAG", "embedding", "VECTOR", "FLAT", 6, "TYPE", "FLOAT32", "DIM", embed.Dimensions, "DISTANCE_METRIC", "COSINE").Err()
	if err != nil {
		s := strings.ToLower(err.Error())
		if strings.Contains(s, "index already exists") {
			return i.validateSchema(ctx)
		}
		if strings.Contains(s, "unknown command") {
			return ErrUnavailable
		}
	}
	return err
}

// Never accept an unrelated or obsolete index under the shared index name.
func (i *Index) validateSchema(ctx context.Context) error {
	info, err := i.rdb.FTInfo(ctx, "idx:lab").Result()
	if err != nil {
		return fmt.Errorf("inspect lab search schema: %w", err)
	}
	incompatible := errors.New("incompatible lab search schema")
	if info.IndexDefinition.KeyType != "HASH" || len(info.IndexDefinition.Prefixes) != 1 || info.IndexDefinition.Prefixes[0] != "lab:" {
		return incompatible
	}
	fields := make(map[string]redis.FTAttribute)
	for _, a := range info.Attributes {
		fields[a.Attribute] = a
	}
	for _, name := range []string{"workspace", "kind", "model"} {
		a, ok := fields[name]
		if !ok || a.Identifier != name || a.Type != "TAG" || a.NoIndex {
			return incompatible
		}
	}
	a, ok := fields["embedding"]
	if !ok || a.Identifier != "embedding" || a.Type != "VECTOR" || a.Algorithm != "FLAT" || a.DataType != "FLOAT32" || a.Dim != embed.Dimensions || a.DistanceMetric != "COSINE" || a.NoIndex {
		return incompatible
	}
	return nil
}
func key(ws uuid.UUID, r labmemory.Ref) string { return "lab:" + ws.String() + ":" + r.String() }
func membersKey(ws uuid.UUID) string           { return "labindex:members:" + ws.String() }
func vector(v []float32) ([]byte, error) {
	if len(v) != embed.Dimensions {
		return nil, errors.New("invalid embedding dimensions")
	}
	b := make([]byte, len(v)*4)
	for j, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nil, errors.New("invalid embedding value")
		}
		binary.LittleEndian.PutUint32(b[j*4:], math.Float32bits(f))
	}
	return b, nil
}
func (i *Index) Put(ctx context.Context, ws uuid.UUID, r labmemory.Ref, version, text string) error {
	return i.PutVersion(ctx, ws, r, version, text)
}

// PutVersion retains the source version for freshness tracking. The index is
// discovery only; callers still re-read the source and check its current version.
func (i *Index) PutVersion(ctx context.Context, ws uuid.UUID, r labmemory.Ref, version, text string) error {
	if strings.TrimSpace(version) == "" {
		return errors.New("record version required")
	}
	v, err := i.e.Embed(ctx, []string{text}, false)
	if err != nil {
		return err
	}
	if len(v) != 1 {
		return errors.New("invalid embedding count")
	}
	b, err := vector(v[0])
	if err != nil {
		return err
	}
	_, err = i.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key(ws, r), "workspace", ws.String(), "kind", r.Kind, "model", i.model, "ref", r.String(), "version", version, "embedding", b)
		p.SAdd(ctx, membersKey(ws), r.String())
		return nil
	})
	return err
}
func (i *Index) Delete(ctx context.Context, ws uuid.UUID, r labmemory.Ref) error {
	_, err := i.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, key(ws, r))
		p.SRem(ctx, membersKey(ws), r.String())
		return nil
	})
	return err
}
func tag(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
func (i *Index) Search(ctx context.Context, ws uuid.UUID, q string, k int) ([]labmemory.Ref, error) {
	if k <= 0 {
		return []labmemory.Ref{}, nil
	}
	if k > 100 {
		k = 100
	}
	v, err := i.e.Embed(ctx, []string{q}, true)
	if err != nil {
		return nil, err
	}
	if len(v) != 1 {
		return nil, errors.New("invalid embedding count")
	}
	b, err := vector(v[0])
	if err != nil {
		return nil, err
	}
	// Filtering model invalidates old-model candidates immediately; the event
	// consumer must backfill all sources when the pinned model changes.
	query := fmt.Sprintf("(@workspace:{%s} @model:{%s})=>[KNN %d @embedding $BLOB AS dist]", tag(ws.String()), tag(i.model), k)
	raw, err := i.rdb.Do(ctx, "FT.SEARCH", "idx:lab", query, "PARAMS", 2, "BLOB", b, "SORTBY", "dist", "ASC", "RETURN", 2, "ref", "workspace", "LIMIT", 0, k, "DIALECT", 2).Result()
	if err != nil {
		return nil, err
	}
	return searchRefs(raw, ws)
}

// VisitStudyCandidates traverses only this workspace's indexed studies, in
// distance order. A page may consist entirely of reopened or deleted studies.
// Embed once, return small pages and continue until the caller has enough.
func (i *Index) VisitStudyCandidates(ctx context.Context, ws uuid.UUID, q string, visit func([]labmemory.Ref) (bool, error)) error {
	v, err := i.e.Embed(ctx, []string{q}, true)
	if err != nil {
		return err
	}
	if len(v) != 1 {
		return errors.New("invalid embedding count")
	}
	b, err := vector(v[0])
	if err != nil {
		return err
	}
	const page = 32
	query := fmt.Sprintf("(@workspace:{%s} @model:{%s} @kind:{study} @embedding:[VECTOR_RANGE 2.01 $BLOB]=>{$YIELD_DISTANCE_AS: dist})", tag(ws.String()), tag(i.model))
	for offset := 0; ; offset += page {
		raw, err := i.rdb.Do(ctx, "FT.SEARCH", "idx:lab", query, "PARAMS", 2, "BLOB", b, "SORTBY", "dist", "ASC", "RETURN", 2, "ref", "workspace", "LIMIT", offset, page, "DIALECT", 2).Result()
		if err != nil {
			return err
		}
		refs, err := searchRefs(raw, ws)
		if err != nil {
			return err
		}
		stop, err := visit(refs)
		if err != nil || stop {
			return err
		}
		// Count raw hits, not validated refs; malformed metadata cannot hide later candidates.
		n := 0
		switch r := raw.(type) {
		case []interface{}:
			n = (len(r) - 1) / 2
		case map[interface{}]interface{}:
			entries, _ := r["results"].([]interface{})
			n = len(entries)
		}
		if n < page {
			return nil
		}
	}
}

func searchRefs(raw any, ws uuid.UUID) ([]labmemory.Ref, error) {
	out := []labmemory.Ref{}
	var rows []interface{}
	switch result := raw.(type) {
	case []interface{}:
		for n := 1; n+1 < len(result); n += 2 {
			rows = append(rows, result[n+1])
		}
	case map[interface{}]interface{}:
		results, _ := result["results"].([]interface{})
		for _, entry := range results {
			if m, ok := entry.(map[interface{}]interface{}); ok {
				rows = append(rows, m["extra_attributes"])
			}
		}
	default:
		return nil, errors.New("invalid search response")
	}
	for _, row := range rows {
		if m, ok := row.(map[interface{}]interface{}); ok {
			var fields []interface{}
			for k, v := range m {
				fields = append(fields, k, v)
			}
			row = fields
		}
		fields, ok := row.([]interface{})
		if !ok {
			continue
		}
		var ref, workspace string
		for j := 0; j+1 < len(fields); j += 2 {
			name, _ := fields[j].(string)
			value, _ := fields[j+1].(string)
			if name == "ref" {
				ref = value
			}
			if name == "workspace" {
				workspace = value
			}
		}
		if workspace != ws.String() {
			continue
		}
		r, err := labmemory.ParseRef(ref)
		if err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}
