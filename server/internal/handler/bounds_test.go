package handler

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/auth"
)

func TestStalledHTTPBodyDoesNotLockWorkspace(t *testing.T) {
	s := newServer(t)
	owner, member := s.signIn(t, "owner@lab.org"), s.signIn(t, "member@lab.org")
	ws := s.workspace(t, owner, "body-lab")
	invitation := s.invite(t, owner, ws.ID, "member@lab.org", "member")
	if code := s.call(t, "POST", "/api/invitations/"+invitation+"/accept", member, "", nil, nil); code != 200 {
		t.Fatal(code)
	}
	memberID := s.userID(t, member)
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	fmt.Fprintf(conn, "POST /api/cards HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nX-Workspace-ID: %s\r\nContent-Length: 100\r\nExpect: 100-continue\r\n\r\n", u.Host, member, ws.ID)
	reader := bufio.NewReader(conn)
	interim, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	interim.Body.Close()
	if interim.StatusCode != 100 {
		t.Fatal(interim.StatusCode)
	}
	fmt.Fprint(conn, `{"question":`)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for _, mutation := range []struct {
		method, path, body string
		status             int
	}{
		{"PATCH", "/api/workspaces/" + ws.ID, `{"name":"unblocked"}`, 200},
		{"DELETE", "/api/workspaces/" + ws.ID + "/members/" + memberID, "", 204},
	} {
		req, err := http.NewRequest(mutation.method, s.URL+mutation.path, strings.NewReader(mutation.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+owner)
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("mutation blocked by incomplete body: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != mutation.status {
			t.Fatal(response.StatusCode)
		}
	}
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("stalled body did not receive bounded response: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("stalled body status: %d", response.StatusCode)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM card WHERE workspace_id=$1", ws.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stalled body created a card")
	}
}

func TestReadJSONRejectsOversizeAndTrailingData(t *testing.T) {
	for _, body := range []string{`{}` + strings.Repeat(" ", (1<<20)-2) + `{}`, `{} {}`} {
		req, err := http.NewRequest("POST", "/", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := readJSON(req, &value); err == nil {
			t.Fatal("accepted oversized or trailing JSON")
		}
	}
	req, _ := http.NewRequest("POST", "/", strings.NewReader(`{}`+strings.Repeat(" ", (1<<20)-2)))
	var value map[string]any
	if err := readJSON(req, &value); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
}

func TestInvitationSMTPClientCancellationRollsBackAndReleasesLock(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "smtp-owner@lab.org")
	ws := s.workspace(t, owner, "smtp-lab")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	s.H.Mail = auth.SMTPMailer{Addr: ln.Addr().String(), From: "owner@lab.org"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.URL+"/api/workspaces/"+ws.ID+"/invitations", strings.NewReader(`{"email":"guest@lab.org","role":"member"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	done := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	var smtp net.Conn
	select {
	case smtp = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("SMTP not contacted")
	}
	defer smtp.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client cancellation stalled")
	}
	smtp.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := smtp.Read(make([]byte, 1)); err == nil {
		t.Fatal("SMTP connection remains open")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("SMTP not closed by cancellation")
	}
	client := &http.Client{Timeout: time.Second}
	req, _ = http.NewRequest("PATCH", s.URL+"/api/workspaces/"+ws.ID, strings.NewReader(`{"name":"released"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("mutation lock retained: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	for _, table := range []string{"workspace_invitation", "event"} {
		query := "SELECT count(*) FROM " + table + " WHERE workspace_id=$1"
		if table == "event" {
			query += " AND type='invitation.created'"
		}
		var count int
		if err := s.Env.Owner.QueryRow(t.Context(), query, ws.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s survived failed SMTP: %d", table, count)
		}
	}
}
