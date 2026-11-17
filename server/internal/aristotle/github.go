// Package aristotle is Aristotle Agent: it fixes GitHub issues assigned to
// its own account, in Lykeion's run sandbox, and opens pull requests for
// review. The GitHub token never enters the sandbox.
package aristotle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const Repo = "lykeion-tech/lykeion"

// Footer discloses that an agent wrote the text, as Multica's Eve does.
const Footer = "\n\n_Written by Aristotle, an agent in Lykeion. It may contain mistakes; a maintainer reviews every change._"

type GitHub struct {
	Base, Token string
	Client      *http.Client
}

func NewGitHub(token string) *GitHub {
	return &GitHub{Base: "https://api.github.com", Token: token, Client: &http.Client{Timeout: 30 * time.Second}}
}

func (g *GitHub) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GitHub %s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

type Account struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// Email is the account's noreply address, which links commits to it.
func (a Account) Email() string { return fmt.Sprintf("%d+%s@users.noreply.github.com", a.ID, a.Login) }

func (g *GitHub) Me(ctx context.Context) (Account, error) {
	var a Account
	err := g.do(ctx, "GET", "/user", nil, &a)
	return a, err
}

type Issue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	PullRequest json.RawMessage `json:"pull_request"`
}

// Assigned lists open issues (not pull requests) assigned to login.
func (g *GitHub) Assigned(ctx context.Context, login string) ([]Issue, error) {
	var all []Issue
	q := url.Values{"assignee": {login}, "state": {"open"}, "per_page": {"100"}, "sort": {"created"}, "direction": {"asc"}}
	if err := g.do(ctx, "GET", "/repos/"+Repo+"/issues?"+q.Encode(), nil, &all); err != nil {
		return nil, err
	}
	issues := all[:0]
	for _, i := range all {
		if i.PullRequest == nil {
			issues = append(issues, i)
		}
	}
	return issues, nil
}

type Comment struct {
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (g *GitHub) Comments(ctx context.Context, issue int) ([]Comment, error) {
	var c []Comment
	err := g.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", Repo, issue), nil, &c)
	return c, err
}

func (g *GitHub) Comment(ctx context.Context, issue int, body string) error {
	return g.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", Repo, issue), map[string]string{"body": body}, nil)
}

// HasPull reports whether any pull request, open or closed, came from branch.
func (g *GitHub) HasPull(ctx context.Context, branch string) (bool, error) {
	var pulls []struct{}
	q := url.Values{"head": {"lykeion-tech:" + branch}, "state": {"all"}}
	err := g.do(ctx, "GET", "/repos/"+Repo+"/pulls?"+q.Encode(), nil, &pulls)
	return len(pulls) > 0, err
}

func (g *GitHub) OpenPull(ctx context.Context, branch, title, body string) (int, error) {
	var p struct {
		Number int `json:"number"`
	}
	err := g.do(ctx, "POST", "/repos/"+Repo+"/pulls", map[string]string{"title": title, "head": branch, "base": "main", "body": body}, &p)
	return p.Number, err
}
