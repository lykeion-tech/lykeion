package skill

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
)

func archiveBundle(archive []byte, filename string) (Bundle, error) {
	switch strings.ToLower(path.Ext(filename)) {
	case ".zip", ".skill":
	default:
		return Bundle{}, fmt.Errorf("only .skill and .zip archives are supported")
	}
	b, err := ParseArchive(archive)
	if err != nil {
		return b, err
	}
	if b.Name == "" {
		b.Name = strings.TrimSpace(strings.TrimSuffix(path.Base(strings.ReplaceAll(filename, "\\", "/")), path.Ext(filename)))
	}
	return metadata(b)
}

func ImportArchive(ctx context.Context, s *Service, workspace, user uuid.UUID, archive []byte, source Source) (Detail, error) {
	b, err := archiveBundle(archive, source.Location)
	if err != nil {
		return Detail{}, invalid(err)
	}
	return s.Create(ctx, workspace, user, b, source)
}

// ImportFolder roots relative browser paths at the directory containing SKILL.md.
func ImportFolder(ctx context.Context, s *Service, workspace, user uuid.UUID, files map[string][]byte) (Detail, error) {
	b, source, err := folderBundle(files)
	if err != nil {
		return Detail{}, invalid(err)
	}
	return s.Create(ctx, workspace, user, b, source)
}
func folderBundle(files map[string][]byte) (Bundle, Source, error) {
	b := Bundle{Files: map[string][]byte{}}
	root := ""
	mains := 0
	for p := range files {
		if !validPath(p) {
			return b, Source{}, fmt.Errorf("unsafe file path %q", p)
		}
		if path.Base(p) == "SKILL.md" {
			mains++
			root = strings.TrimSuffix(p, "SKILL.md")
		}
	}
	if mains != 1 {
		return b, Source{}, fmt.Errorf("folder must contain exactly one SKILL.md")
	}
	for p, data := range files {
		if !strings.HasPrefix(p, root) {
			return b, Source{}, fmt.Errorf("file %q is outside the skill folder", p)
		}
		b.Files[strings.TrimPrefix(p, root)] = data
	}
	location := strings.TrimSuffix(root, "/")
	if root != "" {
		b.Name = path.Base(location)
	} else {
		location = "SKILL.md"
	}
	b, err := metadata(b)
	if err == nil {
		err = Validate(b)
	}
	return b, Source{Kind: "local", Location: location}, err
}
