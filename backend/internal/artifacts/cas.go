// Package artifacts stores immutable bytes. Callers authorize logical artifact
// IDs before using this internal hash address; hashes are never capabilities.
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

func Hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func valid(hash string) bool {
	b, e := hex.DecodeString(hash)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == hash
}
func Put(root string, data []byte) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("artifact root unavailable")
	}
	hash := Hash(data)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(root, "upload-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Chmod(0440); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Link(file.Name(), filepath.Join(root, hash)); err != nil && !os.IsExist(err) {
		return "", err
	}
	_, err = Read(root, hash)
	return hash, err
}
func Read(root, hash string) ([]byte, error) {
	if root == "" || !valid(hash) {
		return nil, errors.New("invalid artifact address")
	}
	data, err := os.ReadFile(filepath.Join(root, hash))
	if err != nil {
		return nil, err
	}
	if Hash(data) != hash {
		return nil, errors.New("artifact integrity failure")
	}
	return data, nil
}
