package github

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

func asciiAlphanumeric(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func Match(prefix, branch, title, body string) []string {
	if prefix == "" {
		return nil
	}
	prefix = strings.ToUpper(prefix)
	pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(prefix) + `-([1-9][0-9]*)`)
	seen := make(map[string]bool)
	var keys []string
	for field, text := range []string{branch, title, body} {
		for _, span := range pattern.FindAllStringSubmatchIndex(text, -1) {
			start, end := span[0], span[1]
			if start > 0 && asciiAlphanumeric(text[start-1]) || end < len(text) && asciiAlphanumeric(text[end]) {
				continue
			}
			if field == 2 && !followsClosingKeyword(text[:start]) {
				continue
			}
			number := text[span[2]:span[3]]
			// Study numbers use PostgreSQL integer columns.
			if _, err := strconv.ParseInt(number, 10, 32); err != nil {
				continue
			}
			key := prefix + "-" + number
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func followsClosingKeyword(before string) bool {
	before = strings.TrimRightFunc(before, unicode.IsSpace)
	if trimmed, ok := strings.CutSuffix(before, ":"); ok {
		before = strings.TrimRightFunc(trimmed, unicode.IsSpace)
	}
	start := len(before)
	for start > 0 && asciiAlphanumeric(before[start-1]) {
		start--
	}
	switch strings.ToLower(before[start:]) {
	case "close", "closes", "closed", "fix", "fixes", "fixed", "resolve", "resolves", "resolved":
		return true
	default:
		return false
	}
}

var pullPath = regexp.MustCompile(`^([A-Za-z0-9-]+)/([A-Za-z0-9_.-]+)/pull/([1-9][0-9]*)(?:/|$)`)

func ParsePRURL(webURL, raw string) (owner, repo string, number int, ok bool) {
	if webURL == "" {
		webURL = defaultWebURL
	}
	base, err := parseBaseURL(webURL)
	if err != nil {
		return "", "", 0, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || !sameOrigin(base, u) {
		return "", "", 0, false
	}
	path, ok := strings.CutPrefix(u.EscapedPath(), strings.TrimSuffix(base.EscapedPath(), "/")+"/")
	if !ok {
		return "", "", 0, false
	}
	match := pullPath.FindStringSubmatch(path)
	if match == nil || match[2] == "." || match[2] == ".." {
		return "", "", 0, false
	}
	for part := range strings.SplitSeq(u.Path, "/") {
		if part == "." || part == ".." || strings.Contains(part, "\\") {
			return "", "", 0, false
		}
	}
	n, err := strconv.ParseInt(match[3], 10, 32)
	if err != nil {
		return "", "", 0, false
	}
	return strings.ToLower(match[1]), strings.ToLower(match[2]), int(n), true
}
