package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lykeion/server/internal/auth"
)

type failedContactMail struct{}

func (failedContactMail) Send(context.Context, string, string, string) error {
	return errors.New("unavailable")
}

func TestContactSales(t *testing.T) {
	s := newServer(t)
	input := map[string]any{"first_name": "Ada", "last_name": "Lovelace", "email": "ada@university.edu", "company": "University", "company_size": "11-50", "country": "Denmark", "purpose": "Research", "goals": "Study biology", "contact_consent": true, "marketing_consent": false, "processing_consent": true}
	post := func() int { return s.call(t, "POST", "/api/contact-sales", "", "", input, nil) }
	if code := post(); code != 204 {
		t.Fatalf("valid contact: %d", code)
	}
	if body := s.Mail.last["noreply@lykeion.tech"]; !strings.Contains(body, "ada@university.edu") || !strings.Contains(body, "Study biology") || !strings.Contains(body, "Marketing consent: false") {
		t.Fatalf("contact mail missing details: %q", body)
	}
	for _, email := range []string{"ada@gmail.com", "ada@outlook.com", "not-an-address", "Ada <ada@university.edu>"} {
		input["email"] = email
		if code := post(); code != 400 {
			t.Errorf("email %q: %d", email, code)
		}
	}
	input["email"] = "ada@university.edu"
	input["first_name"] = "  "
	if code := post(); code != 400 {
		t.Errorf("missing name: %d", code)
	}
	input["first_name"] = "Ada"
	input["processing_consent"] = false
	if code := post(); code != 400 {
		t.Errorf("missing consent: %d", code)
	}
	input["processing_consent"] = true
	input["goals"] = strings.Repeat("x", 4001)
	if code := post(); code != 400 {
		t.Errorf("oversized goals: %d", code)
	}
	input["goals"] = ""
	s.H.Mail = auth.LogMailer{}
	if code := post(); code != 503 {
		t.Errorf("log-only mailer: %d", code)
	}
	s.H.Mail = failedContactMail{}
	if code := post(); code != 503 {
		t.Errorf("failed mailer: %d", code)
	}
	s.H.Limit.R = nil
	if code := post(); code != 503 {
		t.Errorf("unavailable limiter: %d", code)
	}
}

func TestContactSalesRateLimit(t *testing.T) {
	s := newServer(t)
	input := map[string]any{"first_name": "A", "last_name": "B", "email": "a@university.edu", "company": "Lab", "company_size": "1-10", "country": "Denmark", "purpose": "Research", "processing_consent": true}
	for i := 0; i < 10; i++ {
		if code := s.call(t, "POST", "/api/contact-sales", "", "", input, nil); code != 204 {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	for i := 0; i < 100; i++ {
		if code := s.call(t, "POST", "/api/contact-sales", "", "", input, nil); code != 429 {
			t.Fatalf("rate limit: %d", code)
		}
	}
	if n, err := s.Env.Redis.Get(t.Context(), "lyk:rl:contact:global").Int(); err != nil || n != 10 {
		t.Fatalf("rejected IP requests consumed global limit: %d, %v", n, err)
	}
}
