package coresearch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// OpenSnapshot checks a closed private spool before exposing its immutable bytes.
func OpenSnapshot(path string, size int64, sha string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return nil, errors.New("snapshot identity changed")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		f.Close()
		return nil, errors.New("snapshot identity changed")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, size+1))
	if err != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != sha {
		f.Close()
		return nil, errors.New("snapshot contents changed")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
