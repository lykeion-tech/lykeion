package coresearch

import (
	"fmt"
	"strings"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

func TestPromptFencesHistoryAndMessage(t *testing.T) {
	files := make([]FileRow, 501)
	for i := range files {
		files[i] = FileRow{Path: fmt.Sprintf("file-%03d", i), N: 2, Size: 100}
	}
	p := Prompt(PromptIn{ProjectName: "P", Files: files, Mode: "experiment", Message: "new-message", History: []db.CoresearchMessage{{Kind: "comment", Body: "old-message"}}})
	for _, body := range []string{"old-message", "new-message"} {
		start := strings.Index(p, body)
		if start < 0 || (strings.LastIndex(p[:start], "-BEGIN\n") < 0 || strings.LastIndex(p[:start], "-BEGIN\n") < strings.LastIndex(p[:start], "-END\n")) || !strings.Contains(p[start:], "-END") {
			t.Fatalf("unfenced %s: %s", body, p)
		}
	}
	if !strings.Contains(p, "file-499") || strings.Contains(p, "file-500") || !strings.Contains(p, "… and 1 more") || !strings.Contains(p, "lyk cell run") {
		t.Fatal(p)
	}
}

func TestPromptUsesLastTwentyMessages(t *testing.T) {
	history := make([]db.CoresearchMessage, 21)
	for i := range history {
		history[i] = db.CoresearchMessage{Kind: "comment", Body: fmt.Sprintf("history-%02d", i)}
	}
	p := Prompt(PromptIn{History: history, Message: "current"})
	if strings.Contains(p, "history-00") {
		t.Fatal("included history older than twenty messages")
	}
	for i := 1; i <= 20; i++ {
		if !strings.Contains(p, fmt.Sprintf("history-%02d", i)) {
			t.Fatalf("missing message %d", i)
		}
	}
}

func TestCowriteSelectionPrompt(t *testing.T) {
	p := Prompt(PromptIn{Mode: "cowrite", Selection: &Selection{Path: "main.tex", Version: 7, From: 1, To: 3}})
	for _, s := range []string{"Cowrite", "lyk paper get", "lyk paper edit", "lyk paper build", "main.tex", "\"version\":7", "\"from\":1", "\"to\":3"} {
		if !strings.Contains(p, s) {
			t.Fatalf("missing %s in %s", s, p)
		}
	}
}

func TestPromptNamesSelectedCapabilitiesForCurrentTurn(t *testing.T) {
	p := Prompt(PromptIn{Message: "analyze", Capabilities: &Capabilities{Skills: []string{"aeon"}, Connectors: []string{"ensembl"}}})
	for _, value := range []string{"aeon", "ensembl"} {
		if !strings.Contains(p, value) {
			t.Fatalf("selected capability %s missing from prompt", value)
		}
	}
}
