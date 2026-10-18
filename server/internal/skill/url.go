package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"
)

func ImportURL(ctx context.Context, s *Service, workspace, user uuid.UUID, raw string) (Detail, error) {
	b, source, err := fetchURLBundle(ctx, http.DefaultClient, raw)
	if err != nil {
		return Detail{}, err
	}
	return s.Create(ctx, workspace, user, b, source)
}

type FetchError struct {
	Status int
	Err    error
}

func (e *FetchError) Error() string { return e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

var errImportCap = errors.New("import size limit exceeded")
var errSourceUnavailable = errors.New("published source is unavailable")

func fetchError(ctx context.Context, err error) error {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, errImportCap):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		err = fmt.Errorf("skill import timed out while fetching source files")
	case errors.Is(err, errSourceUnavailable):
		status = http.StatusServiceUnavailable
	}
	return &FetchError{Status: status, Err: err}
}

type publishedSource struct {
	kind, location, owner, repo, slug string
	segments                          []string
}

func parsePublishedSource(raw string) (publishedSource, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return publishedSource{}, fmt.Errorf("URL is required")
	}
	normalized := raw
	if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
		normalized = "https://" + raw
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return publishedSource{}, fmt.Errorf("invalid published skill URL")
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	source := publishedSource{location: normalized}
	switch strings.ToLower(u.Hostname()) {
	case "clawhub.ai", "www.clawhub.ai":
		source.kind = "clawhub"
		if len(p) < 1 || len(p) > 2 || p[len(p)-1] == "" {
			return source, fmt.Errorf("ClawHub URL must name a skill")
		}
		source.slug = p[len(p)-1]
	case "skills.sh", "www.skills.sh":
		source.kind = "skills_sh"
		if len(p) != 3 {
			return source, fmt.Errorf("Skills.sh URL must be /owner/repository/skill")
		}
		source.owner, source.repo, source.slug = p[0], p[1], p[2]
	case "github.com", "www.github.com":
		source.kind = "github"
		if len(p) < 2 {
			return source, fmt.Errorf("GitHub URL must name owner and repository")
		}
		source.owner, source.repo = p[0], strings.TrimSuffix(p[1], ".git")
		if len(p) > 2 {
			if len(p) < 4 || (p[2] != "tree" && p[2] != "blob") {
				return source, fmt.Errorf("use a canonical GitHub root, tree or SKILL.md blob URL")
			}
			source.segments = p[3:]
			if p[2] == "blob" {
				if len(source.segments) < 2 || !strings.EqualFold(source.segments[len(source.segments)-1], "SKILL.md") {
					return source, fmt.Errorf("GitHub blob URL must point to SKILL.md")
				}
				source.segments = source.segments[:len(source.segments)-1]
			}
		}
	default:
		if !strings.Contains(raw, "/") || !strings.Contains(raw, ".") {
			bare, e := url.Parse(raw)
			if e != nil {
				return source, fmt.Errorf("invalid ClawHub skill path")
			}
			parts := strings.Split(strings.Trim(bare.Path, "/"), "/")
			if len(parts) < 1 || len(parts) > 2 || parts[len(parts)-1] == "" {
				return source, fmt.Errorf("ClawHub path must name a skill")
			}
			source.kind = "clawhub"
			source.slug = parts[len(parts)-1]
			source.location = raw
		} else {
			return source, fmt.Errorf("unsupported source: supported sources are clawhub.ai, skills.sh and github.com")
		}
	}
	for _, s := range append([]string{source.owner, source.repo, source.slug}, source.segments...) {
		if s != "" && (!validPath(s) || strings.Contains(s, "/")) {
			return source, fmt.Errorf("invalid published source path segment %q", s)
		}
	}
	if source.kind != "clawhub" && (source.owner == "" || source.repo == "") {
		return source, fmt.Errorf("owner and repository are required")
	}
	return source, nil
}
func fetchURLBundle(ctx context.Context, client *http.Client, raw string) (Bundle, Source, error) {
	source, err := parsePublishedSource(raw)
	if err != nil {
		return Bundle{}, Source{}, invalid(err)
	}
	var b Bundle
	if source.kind == "clawhub" {
		b, err = fetchClawHub(ctx, client, source)
	} else {
		b, err = fetchGitHub(ctx, client, source)
	}
	provenance := Source{Kind: source.kind, Location: source.location}
	if err != nil {
		return b, provenance, fetchError(ctx, err)
	}
	b, err = metadata(b)
	if err == nil {
		err = Validate(b)
	}
	if err != nil {
		err = invalid(err)
	}
	return b, provenance, err
}
func sourceGet(ctx context.Context, client *http.Client, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Hostname() == "api.github.com" || req.URL.Hostname() == "raw.githubusercontent.com" {
		if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

type sourceHTTPError struct{ Status int }

func (e *sourceHTTPError) Error() string { return fmt.Sprintf("source returned HTTP %d", e.Status) }
func sourceJSON(ctx context.Context, client *http.Client, target string, out any) error {
	resp, err := sourceGet(ctx, client, target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &sourceHTTPError{Status: resp.StatusCode}
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("invalid source response: %w", err)
	}
	return nil
}
func sourceFile(ctx context.Context, client *http.Client, target, p string) ([]byte, error) {
	resp, err := sourceGet(ctx, client, target)
	if err != nil {
		return nil, fmt.Errorf("file %q: %w", p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file %q: source returned HTTP %d", p, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, FileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("file %q: %w", p, err)
	}
	if len(data) > FileBytes {
		return nil, fmt.Errorf("%w: file %q exceeds %d bytes", errImportCap, p, FileBytes)
	}
	return data, nil
}
func fetchClawHub(ctx context.Context, client *http.Client, s publishedSource) (Bundle, error) {
	b := Bundle{Name: s.slug, Files: map[string][]byte{}}
	base := "https://clawhub.ai/api/v1/skills/" + url.PathEscape(s.slug)
	var detail struct {
		Skill struct {
			DisplayName string            `json:"displayName"`
			Tags        map[string]string `json:"tags"`
		} `json:"skill"`
		LatestVersion *struct {
			Version string `json:"version"`
		} `json:"latestVersion"`
	}
	if err := sourceJSON(ctx, client, base, &detail); err != nil {
		return b, err
	}
	if detail.Skill.DisplayName != "" {
		b.Name = detail.Skill.DisplayName
	}
	version := detail.Skill.Tags["latest"]
	if version == "" && detail.LatestVersion != nil {
		version = detail.LatestVersion.Version
	}
	if version == "" {
		return b, fmt.Errorf("ClawHub did not provide a published version")
	}
	var listing struct {
		Version struct {
			Files []struct {
				Path string `json:"path"`
				Size int64  `json:"size"`
			} `json:"files"`
		} `json:"version"`
	}
	if err := sourceJSON(ctx, client, base+"/versions/"+url.PathEscape(version), &listing); err != nil {
		return b, err
	}
	for _, f := range listing.Version.Files {
		if err := checkImportEntry(b, f.Path, f.Size); err != nil {
			return b, err
		}
		data, err := sourceFile(ctx, client, base+"/file?path="+url.QueryEscape(f.Path)+"&version="+url.QueryEscape(version), f.Path)
		if err != nil {
			return b, err
		}
		b.Files[f.Path] = data
		if err = checkImportBytes(b); err != nil {
			return b, err
		}
	}
	return b, nil
}
func checkImportEntry(b Bundle, p string, size int64) error {
	if !validPath(p) {
		return fmt.Errorf("unsafe file path %q", p)
	}
	if _, ok := b.Files[p]; ok {
		return fmt.Errorf("duplicate file path %q", p)
	}
	if size < 0 {
		return fmt.Errorf("file %q has invalid size", p)
	}
	if size > FileBytes {
		return fmt.Errorf("%w: file %q exceeds size limit", errImportCap, p)
	}
	if len(b.Files) >= SupportingFiles+1 {
		return fmt.Errorf("%w: file %q exceeds supporting file count", errImportCap, p)
	}
	if p != "SKILL.md" && strings.EqualFold(path.Base(p), "SKILL.md") {
		return fmt.Errorf("file %q is an additional SKILL.md", p)
	}
	return nil
}
func checkImportBytes(b Bundle) error {
	total, count := 0, 0
	for p, data := range b.Files {
		if p != "SKILL.md" {
			total += len(data)
			count++
		}
	}
	if total > SupportingBytes || count > SupportingFiles {
		return fmt.Errorf("%w: skill exceeds supporting file limits", errImportCap)
	}
	return nil
}
func escapeSourcePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

type githubEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Mode string `json:"mode"`
	Size int64  `json:"size"`
}

func fetchGitHub(ctx context.Context, client *http.Client, s publishedSource) (Bundle, error) {
	b := Bundle{Files: map[string][]byte{}}
	api := "https://api.github.com/repos/" + url.PathEscape(s.owner) + "/" + url.PathEscape(s.repo)
	ref, dir := "", ""
	if len(s.segments) > 0 {
		blocked := false
		for n := len(s.segments); n > 0; n-- {
			candidate := strings.Join(s.segments[:n], "/")
			resp, err := sourceGet(ctx, client, api+"/commits/"+escapeSourcePath(candidate))
			if err != nil {
				return b, err
			}
			resp.Body.Close()
			switch resp.StatusCode {
			case 200:
				ref = candidate
				dir = strings.Join(s.segments[n:], "/")
			case 401, 403, 429:
				blocked = true
			case 404, 422:
			default:
				return b, fmt.Errorf("GitHub ref probe returned HTTP %d", resp.StatusCode)
			}
			if ref != "" {
				break
			}
		}
		if ref == "" {
			if !blocked {
				return b, fmt.Errorf("could not resolve GitHub ref")
			}
			ref = s.segments[0]
			dir = strings.Join(s.segments[1:], "/")
		}
	} else {
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := sourceJSON(ctx, client, api, &repo); err == nil {
			ref = repo.DefaultBranch
		}
		if ref == "" {
			ref = "main"
		}
	}
	rawBase := "https://raw.githubusercontent.com/" + url.PathEscape(s.owner) + "/" + url.PathEscape(s.repo) + "/" + escapeSourcePath(ref) + "/"
	var tree struct {
		Tree      []githubEntry `json:"tree"`
		Truncated bool          `json:"truncated"`
	}
	treeErr := sourceJSON(ctx, client, api+"/git/trees/"+url.PathEscape(ref)+"?recursive=1", &tree)
	if s.kind == "skills_sh" {
		if treeErr != nil {
			return b, fmt.Errorf("%w: could not enumerate Skills.sh repository: %v", errSourceUnavailable, treeErr)
		}
		var err error
		candidates := tree.Tree
		if tree.Truncated {
			candidates = nil
			for _, prefix := range []string{"skills", ".claude/skills", "plugin/skills"} {
				entries, e := crawlGitHub(ctx, client, api, ref, prefix)
				if e != nil {
					var status *sourceHTTPError
					if errors.As(e, &status) && status.Status == 404 {
						continue
					}
					return b, e
				}
				candidates = append(candidates, entries...)
			}
		}
		dir, err = skillsShDirectory(ctx, client, rawBase, s.slug, candidates)
		if err != nil && tree.Truncated {
			for _, candidate := range []string{"skills/" + s.slug, ".claude/skills/" + s.slug, "plugin/skills/" + s.slug, s.slug} {
				data, e := sourceFile(ctx, client, rawBase+escapeSourcePath(candidate+"/SKILL.md"), candidate+"/SKILL.md")
				if e == nil && len(data) > 0 {
					dir = candidate
					err = nil
					break
				}
				if ctx.Err() != nil {
					return b, ctx.Err()
				}
			}
		}
		if err != nil {
			return b, err
		}
	}
	b.Name = s.repo
	if dir != "" {
		b.Name = path.Base(dir)
	}
	if s.kind == "skills_sh" {
		b.Name = s.slug
	}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	if treeErr != nil || tree.Truncated {
		entries, err := crawlGitHub(ctx, client, api, ref, dir)
		if err != nil {
			return b, err
		}
		tree.Tree = entries
	}
	sizes := map[string]int64{}
	total, count := int64(0), 0
	for _, entry := range tree.Tree {
		if !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		p := strings.TrimPrefix(entry.Path, prefix)
		if entry.Type == "tree" {
			continue
		}
		if entry.Type != "blob" || (entry.Mode != "" && entry.Mode != "100644" && entry.Mode != "100755") {
			return b, fmt.Errorf("file %q is not a regular file", p)
		}
		if err := checkImportEntry(b, p, entry.Size); err != nil {
			return b, err
		}
		if _, exists := sizes[p]; exists {
			return b, fmt.Errorf("duplicate file path %q", p)
		}
		sizes[p] = entry.Size
		if p != "SKILL.md" {
			count++
			total += entry.Size
		}
	}
	if count > SupportingFiles || total > SupportingBytes {
		return b, fmt.Errorf("%w: skill exceeds supporting file limits", errImportCap)
	}
	if _, ok := sizes["SKILL.md"]; !ok {
		return b, fmt.Errorf("SKILL.md is missing from selected GitHub folder")
	}
	paths := make([]string, 0, len(sizes))
	for p := range sizes {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		data, err := sourceFile(ctx, client, rawBase+escapeSourcePath(prefix+p), p)
		if err != nil {
			return b, err
		}
		b.Files[p] = data
		if err = checkImportBytes(b); err != nil {
			return b, err
		}
	}
	return b, nil
}
func skillsShDirectory(ctx context.Context, client *http.Client, rawBase, slug string, tree []githubEntry) (string, error) {
	paths := []string{}
	for _, e := range tree {
		if e.Type == "blob" && path.Base(e.Path) == "SKILL.md" {
			paths = append(paths, e.Path)
		}
	}
	preferred, remaining := []string{}, []string{}
	for _, p := range paths {
		if preferredSkillPath(slug, p) {
			preferred = append(preferred, p)
		} else {
			remaining = append(remaining, p)
		}
	}
	paths = append(preferred, remaining...)
	for _, p := range paths {
		data, err := sourceFile(ctx, client, rawBase+escapeSourcePath(p), p)
		if err != nil {
			return "", err
		}
		parsed, err := parseMetadata(Bundle{Files: map[string][]byte{"SKILL.md": data}}, true)
		if err == nil && parsed.Name == slug {
			dir := path.Dir(p)
			if dir == "." {
				dir = ""
			}
			return dir, nil
		}
	}
	for _, p := range []string{"skills/" + slug + "/SKILL.md", ".claude/skills/" + slug + "/SKILL.md", "plugin/skills/" + slug + "/SKILL.md", slug + "/SKILL.md"} {
		if slices.Contains(paths, p) {
			return path.Dir(p), nil
		}
	}
	return "", fmt.Errorf("SKILL.md for Skills.sh skill %q was not found", slug)
}
func crawlGitHub(ctx context.Context, client *http.Client, api, ref, dir string) ([]githubEntry, error) {
	var listing []struct {
		Path      string `json:"path"`
		Type      string `json:"type"`
		Size      int64  `json:"size"`
		Submodule string `json:"submodule_git_url"`
		Target    string `json:"target"`
	}
	if err := sourceJSON(ctx, client, api+"/contents/"+escapeSourcePath(dir)+"?ref="+url.QueryEscape(ref), &listing); err != nil {
		return nil, fmt.Errorf("folder %q: %w", dir, err)
	}
	out := []githubEntry{}
	for _, e := range listing {
		if !validPath(e.Path) || path.Dir(e.Path) != dir && !(dir == "" && path.Dir(e.Path) == ".") {
			return nil, fmt.Errorf("unsafe directory listing path %q", e.Path)
		}
		if e.Type == "dir" {
			children, err := crawlGitHub(ctx, client, api, ref, e.Path)
			if err != nil {
				return nil, err
			}
			out = append(out, children...)
		} else {
			if e.Type != "file" || e.Submodule != "" || e.Target != "" {
				return nil, fmt.Errorf("file %q is not a regular file", e.Path)
			}
			out = append(out, githubEntry{Path: e.Path, Type: "blob", Mode: "100644", Size: e.Size})
		}
	}
	return out, nil
}

func preferredSkillPath(slug, p string) bool {
	dir := strings.ToLower(path.Dir(p))
	base := path.Base(dir)
	parts := strings.Split(strings.ToLower(slug), "-")
	hints := []string{strings.ToLower(slug)}
	for i := 1; i < len(parts); i++ {
		hints = append(hints, strings.Join(parts[i:], "-"))
	}
	hints = append(hints, parts...)
	for _, hint := range hints {
		if len(hint) >= 3 && (strings.Contains(dir, hint) || strings.Contains(base, hint) || strings.Contains(hint, base)) {
			return true
		}
	}
	return false
}
