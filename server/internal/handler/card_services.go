package handler

import (
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type newService struct {
	Name         string    `json:"name"`
	AttachmentID uuid.UUID `json:"attachment_id"`
	Start        string    `json:"start"`
	Client       string    `json:"client"`
	Env          []string  `json:"env"`
}

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func checkServices(services []newService, env map[string]string) error {
	if len(services) > 4 {
		return cardInputError("a study can have at most 4 hidden services")
	}
	names := map[string]bool{}
	ids := map[uuid.UUID]bool{}
	for _, s := range services {
		if !serviceName.MatchString(s.Name) {
			return cardInputError("invalid hidden service name: " + s.Name)
		}
		if names[s.Name] {
			return cardInputError("duplicate hidden service name: " + s.Name)
		}
		names[s.Name] = true
		if s.AttachmentID == uuid.Nil || ids[s.AttachmentID] {
			return cardInputError("a hidden service must name its own pending upload")
		}
		ids[s.AttachmentID] = true
		if strings.TrimSpace(s.Start) == "" || strings.ContainsRune(s.Start, 0) {
			return cardInputError("hidden service start command required")
		}
		if s.Client != "" && (strings.HasPrefix(s.Client, "/") || strings.ContainsAny(s.Client, "\\\x00") || path.Clean(s.Client) == "." || hasParent(s.Client)) {
			return cardInputError("hidden service client must be a relative file path with no .. segment")
		}
		for _, name := range s.Env {
			if _, ok := env[name]; !ok {
				return cardInputError("hidden service variable is not a study variable: " + name)
			}
		}
	}
	return nil
}
func hasParent(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return true
		}
	}
	return false
}
func pendingServices(r *http.Request, tx *store.Tx, ws, user uuid.UUID, services []newService) ([]db.Attachment, error) {
	if len(services) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(services))
	for i, s := range services {
		ids[i] = s.AttachmentID
	}
	rows, err := tx.Q.PendingAttachments(r.Context(), db.PendingAttachmentsParams{WorkspaceID: ws, UploaderID: user, Ids: ids})
	if err != nil {
		return nil, err
	}
	if len(rows) != len(services) {
		return nil, cardInputError("Unknown hidden service upload")
	}
	for _, row := range rows {
		if row.Kind != "service" {
			return nil, cardInputError("Unknown hidden service upload")
		}
	}
	return rows, nil
}
func linkStudyServices(r *http.Request, tx *store.Tx, card db.Card, user uuid.UUID, services []newService) error {
	for _, s := range services {
		n, err := tx.Q.LinkService(r.Context(), db.LinkServiceParams{WorkspaceID: card.WorkspaceID, UploaderID: user, ID: s.AttachmentID, CardID: &card.ID, ServiceName: &s.Name, ServiceStart: &s.Start, ServiceClient: &s.Client, ServiceEnv: s.Env})
		if err != nil {
			return err
		}
		if n == 0 {
			return errFileTaken
		}
	}
	return nil
}
