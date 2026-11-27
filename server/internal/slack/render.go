package slack

import (
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/labmemory"
)

const maxSlackText = 3900

var mrkdwnEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// appURL is where the dashboard (a hash router) is served.
func appURL(publicURL string) string { return strings.TrimRight(publicURL, "/") + "/app/#" }

func link(publicURL string, r labmemory.DisplayRef) string {
	p := labmemory.RefPath(r)
	if p == "" {
		return mrkdwnEscaper.Replace(r.Title)
	}
	return "<" + appURL(publicURL) + p + "|" + strings.NewReplacer("|", "¦").Replace(mrkdwnEscaper.Replace(r.Title)) + ">"
}

// Render turns a checked answer into Slack mrkdwn with inline citation links.
func Render(ss []labmemory.DisplayStatement, publicURL string) string {
	var paras []string
	for _, s := range ss {
		p := mrkdwnEscaper.Replace(s.Text)
		for _, c := range s.Cites {
			p += " " + link(publicURL, c)
		}
		paras = append(paras, p)
	}
	out := strings.Join(paras, "\n\n")
	if utf8.RuneCountInString(out) <= maxSlackText {
		return out
	}
	cut := string([]rune(out)[:maxSlackText])
	if i := strings.LastIndexByte(cut, '<'); i > strings.LastIndexByte(cut, '>') {
		cut = cut[:i] // inside a <url|title> link: cut before it
	}
	return cut + "… Open in Lykeion: " + appURL(publicURL) + "/"
}
