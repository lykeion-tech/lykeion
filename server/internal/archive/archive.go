// Package archive validates and extracts study archives without following links.
package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

type Format string

const (
	None   Format = ""
	Zip    Format = "zip"
	Tar    Format = "tar"
	TarGz  Format = "tar.gz"
	TarXz  Format = "tar.xz"
	TarBz2 Format = "tar.bz2"
	TarZst Format = "tar.zst"
)

var (
	ErrUnsafe = errors.New("unsafe archive")
	ErrTooBig = errors.New("archive exceeds limits")
)

const (
	maxBytes = int64(10 << 30)
	maxFiles = 20000
)

type Entry struct {
	Path string
	Size int64
}

// Listing counts unique extracted paths. Entries describes final regular files
// in last-occurrence order, while Bytes includes payloads overwritten later.
type Listing struct {
	Files   int
	Bytes   int64
	Entries []Entry
}

// Kind identifies archive encodings from their bytes, independently of name.
func Kind(head []byte, name string) Format {
	switch {
	case bytes.HasPrefix(head, []byte{'P', 'K', 3, 4}), bytes.HasPrefix(head, []byte{'P', 'K', 5, 6}):
		return Zip
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return TarGz
	case bytes.HasPrefix(head, []byte{0xfd, '7', 'z', 'X', 'Z', 0}):
		return TarXz
	case bytes.HasPrefix(head, []byte("BZh")):
		return TarBz2
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return TarZst
	}
	if len(head) >= 512 {
		if string(head[257:262]) == "ustar" {
			return Tar
		}
		if _, err := tar.NewReader(bytes.NewReader(head)).Next(); err == nil {
			return Tar
		}
		if bytes.Equal(head[:512], make([]byte, 512)) {
			return Tar
		}
	}
	return None
}

func safeName(name string) error {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return ErrUnsafe
	}
	for i, p := range strings.Split(name, "/") {
		if p == ".." || i == 0 && strings.Contains(p, ":") {
			return ErrUnsafe
		}
	}
	return nil
}

// List validates every logical entry and bounds the declared unpacked totals.
func List(filename string, f Format) (Listing, error) {
	return ListContext(context.Background(), filename, f)
}

// ListContext observes cancellation while scanning members and archive metadata.
func ListContext(ctx context.Context, filename string, f Format) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	var out Listing
	budget := pathBudget{paths: make(map[string]struct{}), limit: maxFiles}
	latest := make(map[string]*list.Element)
	var order list.List
	err := walk(ctx, filename, f, func(e Entry, dir bool, r io.Reader) error {
		if err := budget.reserve(e.Path); err != nil {
			return err
		}
		out.Files = len(budget.paths)
		if dir {
			return nil
		}
		if e.Size < 0 || e.Size > maxBytes-out.Bytes {
			return ErrTooBig
		}
		out.Bytes += e.Size
		e.Path = path.Clean(e.Path)
		if previous, exists := latest[e.Path]; exists {
			previous.Value = e
			order.MoveToBack(previous)
		} else {
			latest[e.Path] = order.PushBack(e)
		}
		return nil
	})
	if order.Len() > 0 {
		out.Entries = make([]Entry, 0, order.Len())
		for item := order.Front(); item != nil; item = item.Next() {
			out.Entries = append(out.Entries, item.Value.(Entry))
		}
	}
	return out, err
}

// Extract builds a private partial tree and publishes it only after validation.
func Extract(ctx context.Context, filename string, f Format, dest string, maxBytes int64, maxFiles int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	part := dest + ".part"
	if err := os.Mkdir(part, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(part)
	var total int64
	budget := pathBudget{paths: make(map[string]struct{}), limit: maxFiles}
	err := walk(ctx, filename, f, func(e Entry, dir bool, r io.Reader) error {
		if !dir && (e.Size < 0 || e.Size > maxBytes-total) {
			return ErrTooBig
		}
		if err := budget.reserve(e.Path); err != nil {
			return err
		}
		target := filepath.Join(part, filepath.FromSlash(path.Clean(e.Path)))
		if dir {
			return os.MkdirAll(target, 0700)
		}
		total += e.Size
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(contextReader{ctx, r}, e.Size+1))
		closeErr := out.Close()
		if n > e.Size {
			return ErrTooBig
		}
		if copyErr != nil {
			var disk *os.PathError
			if errors.Is(copyErr, context.Canceled) || errors.Is(copyErr, context.DeadlineExceeded) || errors.As(copyErr, &disk) {
				return copyErr
			}
			return fmt.Errorf("%w: %v", ErrUnsafe, copyErr)
		}
		if n != e.Size {
			return fmt.Errorf("%w: truncated file", ErrUnsafe)
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(part, dest)
}

// pathBudget reserves a member and all implied parents before anything is created.
type pathBudget struct {
	paths map[string]struct{}
	limit int
}

func (b *pathBudget) reserve(name string) error {
	var pending []string
	for p := path.Clean(name); p != "."; p = path.Dir(p) {
		if _, exists := b.paths[p]; exists {
			continue
		}
		if len(b.paths)+len(pending) >= b.limit {
			return ErrTooBig
		}
		pending = append(pending, p)
	}
	for _, p := range pending {
		b.paths[p] = struct{}{}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

type contextReaderAt struct {
	ctx context.Context
	r   io.ReaderAt
}

func (r contextReaderAt) ReadAt(b []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.ReadAt(b, offset)
}

func walk(ctx context.Context, filename string, f Format, visit func(Entry, bool, io.Reader) error) (err error) {
	defer func() {
		if canceled := ctx.Err(); canceled != nil {
			err = canceled
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	if f == Zip {
		stat, err := file.Stat()
		if err != nil {
			return err
		}
		z, err := zip.NewReader(contextReaderAt{ctx, file}, stat.Size())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnsafe, err)
		}
		for _, entry := range z.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := safeName(entry.Name); err != nil {
				return err
			}
			mode := entry.Mode()
			if !mode.IsRegular() && !mode.IsDir() {
				return ErrUnsafe
			}
			if entry.UncompressedSize64 > uint64(^uint64(0)>>1) {
				return ErrTooBig
			}
			rc, err := entry.Open()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrUnsafe, err)
			}
			err = visit(Entry{entry.Name, int64(entry.UncompressedSize64)}, mode.IsDir(), rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	var r io.Reader = contextReader{ctx, file}
	switch f {
	case Tar:
	case TarGz:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnsafe, err)
		}
		defer gz.Close()
		r = gz
	case TarBz2:
		r = bzip2.NewReader(r)
	case TarXz:
		xr, err := xz.NewReader(r)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnsafe, err)
		}
		r = xr
	case TarZst:
		zr, err := zstd.NewReader(r, zstd.WithDecoderLowmem(true), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnsafe, err)
		}
		defer zr.Close()
		r = zr
	default:
		return ErrUnsafe
	}
	padding := &paddingReader{r: contextReader{ctx, r}}
	tr := tar.NewReader(padding)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnsafe, err)
		}
		if err := safeName(h.Name); err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		dir := h.Typeflag == tar.TypeDir
		if !dir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return ErrUnsafe
		}
		if !dir && h.PAXRecords["size"] != "" {
			// archive/tar has validated the override before returning this header.
			size, _ := strconv.ParseInt(h.PAXRecords["size"], 10, 64)
			padding.payload += size - padding.size
			padding.padding = (512 - size%512) % 512
		}
		if err := visit(Entry{h.Name, h.Size}, dir, tr); err != nil {
			return err
		}
	}
}

// paddingReader checks physical tar padding, including extension records,
// while archive/tar interprets the logical headers and names.
type paddingReader struct {
	r       io.Reader
	header  [512]byte
	used    int
	payload int64
	size    int64
	padding int64
}

func (r *paddingReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if r.payload > 0 {
		if int64(len(b)) > r.payload {
			b = b[:r.payload]
		}
		n, err := r.r.Read(b)
		r.payload -= int64(n)
		return n, err
	}
	if r.padding > 0 {
		if int64(len(b)) > r.padding {
			b = b[:r.padding]
		}
		n, err := r.r.Read(b)
		for _, v := range b[:n] {
			if v != 0 {
				return n, ErrUnsafe
			}
		}
		r.padding -= int64(n)
		return n, err
	}
	if len(b) > 512-r.used {
		b = b[:512-r.used]
	}
	n, err := r.r.Read(b)
	copy(r.header[r.used:], b[:n])
	r.used += n
	if r.used == 512 {
		r.used = 0
		size, parseErr := tarSize(r.header[124:136])
		if parseErr != nil {
			return n, ErrUnsafe
		}
		r.size = size
		r.payload = size
		r.padding = (512 - size%512) % 512
	}
	return n, err
}
func tarSize(b []byte) (int64, error) {
	if b[0]&0x80 != 0 {
		if b[0]&0x40 != 0 {
			return 0, ErrUnsafe
		}
		var n int64
		for i, v := range b {
			if i == 0 {
				v &= 0x7f
			}
			if n > (1<<63-1-int64(v))/256 {
				return 0, ErrTooBig
			}
			n = n*256 + int64(v)
		}
		return n, nil
	}
	s := strings.Trim(string(b), " \x00")
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 8, 64)
}
