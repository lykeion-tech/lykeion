package slack

import (
	"errors"

	"github.com/google/uuid"
	"lykeion/server/internal/seal"
)

// ad binds a sealed token to its workspace, installation and kind ("bot" or "app").
func ad(ws, installation uuid.UUID, kind string) []byte {
	return []byte(ws.String() + "/slack/" + installation.String() + "/" + kind)
}

func (s *Service) Seal(ws, installation uuid.UUID, kind, token string) ([]byte, error) {
	b, err := seal.Seal(s.Key, []byte(token), ad(ws, installation, kind))
	return b, err // seal errors never carry the plaintext
}

func (s *Service) Open(ws, installation uuid.UUID, kind string, sealed []byte) (string, error) {
	b, err := seal.Open(s.Key, sealed, ad(ws, installation, kind))
	if err != nil {
		if errors.Is(err, seal.ErrNoKey) {
			return "", err
		}
		return "", errors.New("the Slack token cannot be opened")
	}
	return string(b), nil
}
