package handler

import (
	"lykeion/server/internal/auth"
	"strings"
	"testing"
)

func TestFeedback(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "feedback@example.com")
	post := func(tok, message string) int {
		return s.call(t, "POST", "/api/feedback", tok, "", map[string]string{"message": message}, nil)
	}
	if got := post("", "Hello"); got != 401 {
		t.Fatalf("unauthenticated: %d", got)
	}
	for _, message := range []string{"", "  ", strings.Repeat("x", 10001), "bad\x00text"} {
		if got := post(token, message); got != 400 {
			t.Fatalf("invalid message: %d", got)
		}
	}
	if got := post(token, "A suggestion\nwith details"); got != 204 {
		t.Fatalf("valid: %d", got)
	}
	if got := s.Mail.last["noreply@lykeion.tech"]; got != "A suggestion\nwith details" {
		t.Fatalf("mail: %q", got)
	}
	s.H.Mail = auth.LogMailer{}
	if got := post(token, "Hello"); got != 503 {
		t.Fatalf("log mailer: %d", got)
	}
	s.H.Mail = failedContactMail{}
	if got := post(token, "Hello"); got != 503 {
		t.Fatalf("failed mailer: %d", got)
	}
}

func TestFeedbackRateLimit(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "feedback@example.com")
	for i := 0; i < 11; i++ {
		got := s.call(t, "POST", "/api/feedback", token, "", map[string]string{"message": "Hello"}, nil)
		want := 204
		if i == 10 {
			want = 429
		}
		if got != want {
			t.Fatalf("request %d: got %d, want %d", i, got, want)
		}
	}
}
