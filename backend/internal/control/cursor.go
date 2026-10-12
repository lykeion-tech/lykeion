package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

type cursor struct {
	Workspace string `json:"w"`
	User      string `json:"u"`
	Epoch     int64  `json:"e"`
	Sequence  int64  `json:"s"`
	Expires   int64  `json:"x"`
}

func (s *Server) cursorCipher() cipher.AEAD {
	key := sha256.Sum256(s.Key)
	block, e := aes.NewCipher(key[:])
	if e != nil {
		panic(e)
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		panic(e)
	}
	return g
}

// Encrypt internal positions as well as authenticating them: hidden-event counts
// must not be exposed by merely decoding a signed base64 cursor.
func (s *Server) signCursor(w, u string, epoch, sequence int64) string {
	b, _ := json.Marshal(cursor{w, u, epoch, sequence, time.Now().Add(time.Hour).Unix()})
	g := s.cursorCipher()
	nonce := make([]byte, g.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, b, nil))
}
func (s *Server) parseCursor(value, w, u string, epoch int64) (int64, error) {
	b, e := base64.RawURLEncoding.DecodeString(value)
	g := s.cursorCipher()
	if e != nil || len(b) < g.NonceSize() {
		return 0, errors.New("invalid cursor")
	}
	raw, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	var c cursor
	if e != nil || json.Unmarshal(raw, &c) != nil || c.Workspace != w || c.User != u || c.Epoch != epoch || c.Expires < time.Now().Unix() || c.Sequence < 0 {
		return 0, errors.New("expired or unauthorized cursor")
	}
	return c.Sequence, nil
}
