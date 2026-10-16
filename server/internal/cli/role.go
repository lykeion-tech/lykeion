package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"lykeion/server/internal/apiclient"
)

// roleClient speaks for the running agent run: its token comes from the daemon,
// never from the person's config.
func roleClient() (*apiclient.Client, error) {
	tok, srv := os.Getenv("LYK_TOKEN"), os.Getenv("LYK_SERVER")
	if tok == "" || srv == "" {
		return nil, errors.New("LYK_TOKEN and LYK_SERVER must be set: role commands run inside a Lykeion agent run")
	}
	return &apiclient.Client{Server: srv, Token: tok}, nil
}

func record(cmd *cobra.Command, e *env, kind string, body any) error {
	c, err := roleClient()
	if err != nil {
		return err
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := c.Do(cmd.Context(), "POST", "/api/role/records", map[string]any{"kind": kind, "body": body}, &out); err != nil {
		return err
	}
	if out.Key != "" {
		fmt.Fprintln(e.out, out.Key)
	}
	return nil
}

func stdinText(e *env) (string, error) {
	b, err := io.ReadAll(e.in)
	return string(b), err
}

func groundCmd(e *env) *cobra.Command {
	g := &cobra.Command{Use: "ground", Short: "Grounding role commands"}
	text := func(kind string) *cobra.Command {
		return &cobra.Command{Use: kind, Short: "Record the " + kind + " from stdin", RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := stdinText(e)
			if err != nil {
				return err
			}
			return record(cmd, e, kind, map[string]string{"text": t})
		}}
	}
	var claim, source, quote, stance, accession, note, reason string
	ev := &cobra.Command{Use: "evidence", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "evidence", map[string]string{"claim": claim, "source_id": source, "quote": quote, "stance": stance})
	}}
	ev.Flags().StringVar(&claim, "claim", "", "one-sentence finding")
	ev.Flags().StringVar(&source, "source", "", "source id from search_literature")
	ev.Flags().StringVar(&quote, "quote", "", "exact span from fetch_text")
	ev.Flags().StringVar(&stance, "stance", "", "supports or challenges")
	ds := &cobra.Command{Use: "dataset", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "dataset", map[string]string{"accession": accession, "note": note})
	}}
	ds.Flags().StringVar(&accession, "accession", "", "an opened accession")
	ds.Flags().StringVar(&note, "note", "", "why it fits")
	cp := &cobra.Command{Use: "cannot-proceed", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "cannot_proceed", map[string]string{"reason": reason})
	}}
	cp.Flags().StringVar(&reason, "reason", "", "why")
	g.AddCommand(text("question"), text("notes"), ev, ds, cp)
	return g
}

// resultCmd ends an agent run without the role's usual records: nothing_found is a
// result the gate judges, failed escalates the card.
func resultCmd(e *env) *cobra.Command {
	r := &cobra.Command{Use: "result", Short: "End your run with nothing found, or as failed"}
	var reason string
	failed := &cobra.Command{Use: "failed", Short: "You cannot do the job; a person will look", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "failed", map[string]string{"reason": reason})
	}}
	nothing := &cobra.Command{Use: "nothing-found", Short: "A real search found nothing", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "nothing_found", map[string]string{"reason": reason})
	}}
	for _, c := range []*cobra.Command{failed, nothing} {
		c.Flags().StringVar(&reason, "reason", "", "what happened, specifically")
	}
	r.AddCommand(failed, nothing)
	return r
}

func reviewCmd(e *env) *cobra.Command {
	r := &cobra.Command{Use: "review", Short: "Reviewer commands"}
	var check, result, choice, note, step, to, reason string
	settle := &cobra.Command{Use: "settle", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "review_item", map[string]string{"check": check, "result": result, "choice": choice, "note": note})
	}}
	settle.Flags().StringVar(&check, "check", "", "check id from the draft gate")
	settle.Flags().StringVar(&result, "result", "", "pass or fail")
	settle.Flags().StringVar(&choice, "choice", "", "chosen option, for choice checks")
	settle.Flags().StringVar(&note, "note", "", "why")
	verdict := &cobra.Command{Use: "verdict", RunE: func(cmd *cobra.Command, _ []string) error {
		return record(cmd, e, "verdict", map[string]string{"result": result, "step": step, "to": to, "reason": reason})
	}}
	verdict.Flags().StringVar(&result, "result", "", "pass or fail")
	verdict.Flags().StringVar(&step, "step", "", "role that must redo its part")
	verdict.Flags().StringVar(&to, "to", "", "earlier stage, when the fix belongs there")
	verdict.Flags().StringVar(&reason, "reason", "", "your judgment")
	r.AddCommand(settle, verdict)
	return r
}

func recordsCmd(e *env) *cobra.Command {
	var column string
	cmd := &cobra.Command{Use: "records", Short: "Print the column's live records", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := roleClient()
		if err != nil {
			return err
		}
		var recs []json.RawMessage
		path := "/api/role/records"
		if column != "" {
			path += "?column=" + url.QueryEscape(column)
		}
		if err := c.Do(cmd.Context(), "GET", path, nil, &recs); err != nil {
			return err
		}
		for _, r := range recs {
			fmt.Fprintln(e.out, string(r))
		}
		return nil
	}}
	cmd.Flags().StringVar(&column, "column", "", "default: your run's column")
	return cmd
}

func blobCmd(e *env) *cobra.Command {
	return &cobra.Command{Use: "blob SHA256", Args: cobra.ExactArgs(1), Short: "Print a fetched text or dataset structure", RunE: func(cmd *cobra.Command, a []string) error {
		c, err := roleClient()
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(cmd.Context(), "GET", strings.TrimRight(c.Server, "/")+"/api/role/blobs/"+a[0], nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("blob %s: %s", a[0], resp.Status)
		}
		_, err = io.Copy(e.out, resp.Body)
		return err
	}}
}

func reportCmd(e *env) *cobra.Command {
	return &cobra.Command{Use: "report", Short: "Post this Autoresearch run's report; the text comes on stdin", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := roleClient()
		if err != nil {
			return err
		}
		text, err := stdinText(e)
		if err != nil {
			return err
		}
		if err := c.Do(cmd.Context(), "POST", "/api/role/report", map[string]string{"text": text}, nil); err != nil {
			return err
		}
		fmt.Fprintln(e.out, "Report posted.")
		return nil
	}}
}

func openCardCmd(e *env) *cobra.Command {
	var title string
	cmd := &cobra.Command{Use: "open-card QUESTION", Short: "Open a Backlog card from this Autoresearch run", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			c, err := roleClient()
			if err != nil {
				return err
			}
			var out struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
			}
			if err := c.Do(cmd.Context(), "POST", "/api/role/cards", map[string]string{"question": strings.Join(a, " "), "title": title}, &out); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "#%d %s\n", out.Number, out.Title)
			return nil
		}}
	cmd.Flags().StringVar(&title, "title", "", "short title (default: from the question)")
	return cmd
}
