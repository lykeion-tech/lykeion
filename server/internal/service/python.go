package service

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var pythonVersion = regexp.MustCompile(`^3\.(9|1[0-4])$`)

func ValidPythonVersion(version string) bool { return pythonVersion.MatchString(version) }

func StudyPython(ctx context.Context, tx *store.Tx, card uuid.UUID) (string, error) {
	records, err := tx.Q.CardKindRecords(ctx, db.CardKindRecordsParams{WorkspaceID: tx.WS, CardID: card, Kind: "python_version"})
	if err != nil || len(records) == 0 {
		return "", err
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(records[len(records)-1].Body, &body); err != nil {
		return "", err
	}
	if !ValidPythonVersion(body.Version) {
		return "", errors.New("invalid recorded Python version")
	}
	return body.Version, nil
}
