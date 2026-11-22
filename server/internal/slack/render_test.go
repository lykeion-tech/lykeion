package slack

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/labmemory"
)

func TestRenderEscapesAndCuts(t *testing.T) {
	got := Render([]labmemory.DisplayStatement{{Kind: "gap", Text: "a<b & c>"}}, nil, "https://lyk.test")
	if got != "a&lt;b &amp; c&gt;" {
		t.Fatalf("%q", got)
	}
	long := Render([]labmemory.DisplayStatement{{Text: strings.Repeat("x", 5000)}}, nil, "https://lyk.test")
	if n := utf8.RuneCountInString(long); n > 4000 || !strings.HasSuffix(long, "… Open in Lykeion: https://lyk.test/app/#/") {
		t.Fatalf("len=%d %q", n, long[len(long)-60:])
	}
}

func TestRenderCitations(t *testing.T) {
	study := uuid.New()
	cite := labmemory.DisplayRef{Ref: "study:" + study.String(), Title: "Study <1>", StudyID: &study}
	got := Render([]labmemory.DisplayStatement{{Text: "one", Cites: []labmemory.DisplayRef{cite}}, {Text: "two"}}, []labmemory.DisplayRef{cite}, "https://lyk.test/")
	want := "one <https://lyk.test/app/#/studies/" + study.String() + "|Study &lt;1&gt;>\n\ntwo\n\nSources: <https://lyk.test/app/#/studies/" + study.String() + "|Study &lt;1&gt;>"
	if got != want {
		t.Fatalf("%q\n%q", got, want)
	}
}

func TestRenderCutNeverSplitsLink(t *testing.T) {
	study := uuid.New()
	c := labmemory.DisplayRef{Ref: "study:" + study.String(), Title: "T", StudyID: &study}
	got := Render([]labmemory.DisplayStatement{{Text: strings.Repeat("x", 3890), Cites: []labmemory.DisplayRef{c}}}, nil, "https://lyk.test")
	if strings.Contains(got, "<") || !strings.Contains(got, "… Open in Lykeion") {
		t.Fatalf("%q", got[3800:])
	}
}
