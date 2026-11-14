package coresearch

import "testing"

func TestAttachmentPath(t *testing.T) {
	for _, mode := range []string{"experiment", "cowrite"} {
		want := "attachments/plot.png"
		if mode == "cowrite" {
			want = "figures/plot.png"
		}
		got, err := AttachPath(mode, "plot.png")
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", mode, got, err)
		}
		for _, name := range []string{"../x", ".claude/x", "/x", ".git/config"} {
			if _, err := AttachPath(mode, name); err == nil {
				t.Fatalf("unsafe %s", name)
			}
		}
	}
}
