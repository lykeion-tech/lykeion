package coresearch

import "testing"

func TestParseStudyDraft(t *testing.T) {
	const prefix = "Ready to proceed?\n\n```study-draft\n"
	const suffix = "\n```"
	const fields = `,"title":"Street heat","description":"Compare matched streets","stage":"grounding"}`
	for _, tc := range []struct {
		name, text string
		prior      int
		want       bool
	}{
		{"offer on first message", prefix + `{"trigger":"offer"` + fields + suffix, 0, false},
		{"offer after discussion", prefix + `{"trigger":"offer"` + fields + suffix, 1, true},
		{"explicit request", prefix + `{"trigger":"request"` + fields + suffix, 0, true},
		{"bad stage", prefix + `{"trigger":"request","title":"Heat","description":"Compare","stage":"done"}` + suffix, 2, false},
		{"nonfinal block", prefix + `{"trigger":"offer"` + fields + suffix + "\nMore text", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := ParseStudyDraft(tc.text, tc.prior)
			if (draft != nil) != tc.want {
				t.Fatalf("draft = %#v, want present %t", draft, tc.want)
			}
			if draft != nil && draft.Prose != "Ready to proceed?" {
				t.Fatalf("prose = %q", draft.Prose)
			}
		})
	}
}
