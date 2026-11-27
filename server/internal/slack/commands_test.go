package slack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type fakeDrafter struct {
	got []DraftIn
	err error
	id  uuid.UUID
}

func (d *fakeDrafter) SlackDraft(_ context.Context, _, _ uuid.UUID, in DraftIn) (uuid.UUID, error) {
	d.got = append(d.got, in)
	return d.id, d.err
}

func (f *inFix) cmd(t *testing.T, c Command) (string, []byte) {
	t.Helper()
	if c.Channel == "" {
		c.Channel = "C1"
	}
	text, blocks, err := f.S.HandleCommand(context.Background(), f.Inst, c)
	if err != nil {
		t.Fatal(err)
	}
	return text, blocks
}

func TestStudyCommandDrafts(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	study := uuid.New()
	d := &fakeDrafter{id: study}
	f.S.Drafter = d
	if _, err := f.Env.Owner.Exec(context.Background(), `INSERT INTO autoresearch(id,workspace_id,owner_id,title,execution_mode) VALUES($1,$2,$3,'s','run_only')`, study, f.WS, u); err != nil {
		t.Fatal(err)
	}
	text, _ := f.cmd(t, Command{Name: "/to-do", Text: " glucose and sleep ", User: "UA"})
	if text != "Drafting your study. I'll DM you the link." {
		t.Fatal(text)
	}
	if len(d.got) != 1 || d.got[0].Prompt != "glucose and sleep" || d.got[0].Priority != "none" {
		t.Fatalf("%+v", d.got)
	}
	if n := f.count(t, `SELECT count(*) FROM slack_post WHERE kind='draft' AND user_id=$1 AND autoresearch_id=$2 AND channel='D1' AND thread_ts=''`, u, study); n != 1 {
		t.Fatalf("posts=%d", n)
	}
}

func TestStudyCommandUnusableAgent(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.S.Drafter = &fakeDrafter{err: UserError("Connect your machine and try again.")}
	text, _ := f.cmd(t, Command{Name: "/to-do", Text: "x", User: "UA"})
	if text != "Connect your machine and try again." || f.count(t, `SELECT count(*) FROM slack_post`) != 0 {
		t.Fatal(text)
	}
	f.S.Drafter = &fakeDrafter{err: errors.New("db down xoxb-1-bot")}
	text, _, err := f.S.HandleCommand(context.Background(), f.Inst, Command{Name: "/to-do", Text: "x", User: "UA"})
	if text != "Something went wrong. Try again." || err == nil {
		t.Fatal(text, err)
	}
}

func TestStudyUsageAndUnbound(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	for _, name := range []string{"/to-do", "/study"} {
		if text, _ := f.cmd(t, Command{Name: name, User: "UA"}); text != "Usage: /to-do <what you want to study>" {
			t.Fatalf("%s: %q", name, text)
		}
	}
	text, _ := f.cmd(t, Command{Name: "/new", User: "U9"})
	if !strings.Contains(text, "/app/#/slack/link?") {
		t.Fatal(text)
	}
}

func (f *inFix) dmChat(t *testing.T, user string) {
	f.send(t, Message{User: user, Text: "hello"})
}

func TestNewForgetsDMChat(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	f.dmChat(t, "UA")
	if text, _ := f.cmd(t, Command{Name: "/new", User: "UA"}); text != "Your next message starts a new conversation." {
		t.Fatal(text)
	}
	if n := f.count(t, `SELECT count(*) FROM slack_chat WHERE user_id=$1 AND thread_ts=''`, u); n != 0 {
		t.Fatalf("dm mapping kept: %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_chat WHERE owner_id=$1 AND deleted_at IS NULL`, u); n != 1 {
		t.Fatal("chat itself must stay")
	}
	f.dmChat(t, "UA")
	if n := f.count(t, `SELECT count(*) FROM lab_chat WHERE owner_id=$1`, u); n != 2 {
		t.Fatalf("chats=%d", n)
	}
}

func TestClearAsksThenDeletes(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	f.dmChat(t, "UA")
	var deleted []uuid.UUID
	f.S.DeleteChat = func(ctx context.Context, tx *store.Tx, user uuid.UUID, chat db.LabChat) error {
		if user != u {
			t.Errorf("user %s", user)
		}
		deleted = append(deleted, chat.ID)
		return nil
	}
	text, blocks := f.cmd(t, Command{Name: "/clear", User: "UA"})
	if len(deleted) != 0 || text == "" || !strings.Contains(string(blocks), `"slack_clear_confirm"`) {
		t.Fatalf("%q %s", text, blocks)
	}
	got, err := f.S.HandleAction(context.Background(), f.Inst, Action{ID: "slack_clear_confirm", User: "UA"})
	if err != nil || got != "Conversation deleted." || len(deleted) != 1 {
		t.Fatalf("%q %v %v", got, err, deleted)
	}
	if n := f.count(t, `SELECT count(*) FROM slack_chat WHERE user_id=$1 AND thread_ts=''`, u); n != 0 {
		t.Fatal("mapping kept")
	}
	// Nothing left to delete.
	got, err = f.S.HandleAction(context.Background(), f.Inst, Action{ID: "slack_clear_confirm", User: "UA"})
	if err != nil || got != "There is no conversation to delete." || len(deleted) != 1 {
		t.Fatalf("%q %v", got, err)
	}
}

func TestHelpText(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	want := "DM me, or @mention me in a channel, to ask about your workspace. Answers in a channel are visible to everyone in it, including people outside your Lykeion workspace. /to-do <request> drafts a study in To-do. /new starts a new conversation. /clear deletes the current one."
	if text, _ := f.cmd(t, Command{Name: "/help", User: "UA"}); text != want {
		t.Fatal(text)
	}
}
