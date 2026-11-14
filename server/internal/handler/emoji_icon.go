package handler

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"sort"
	"strings"
)

//go:embed emoji_hashes.bin
var emojiHashes []byte

func validEmojiAvatar(value string) bool {
	if value == "" {
		return true
	}
	emoji, ok := strings.CutPrefix(value, "emoji:")
	if !ok {
		return false
	}
	hash := sha256.Sum256([]byte(emoji))
	i := sort.Search(len(emojiHashes)/sha256.Size, func(i int) bool {
		return bytes.Compare(emojiHashes[i*sha256.Size:(i+1)*sha256.Size], hash[:]) >= 0
	})
	return i < len(emojiHashes)/sha256.Size && bytes.Equal(emojiHashes[i*sha256.Size:(i+1)*sha256.Size], hash[:])
}
