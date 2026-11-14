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
	var out map[string]any
	if err := c.Do(cmd.Context(), "POST", "/api/role/records", map[string]any{"kind": kind, "body": body}, &out); err != nil {
		return err
	}
	key, _ := out["key"].(string)
	return emit(cmd, e, out, key)
}

func stdinText(e *env) (string, error) {
	b, err := io.ReadAll(e.in)
	return string(b), err
}

// resultCmd ends a member's run without work; its leader is woken with the
// reason.
func resultCmd(e *env) *cobra.Command {
	r := &cobra.Command{Use: "result", Short: "End your run as failed"}
	var reason string
	failed := &cobra.Command{Use: "failed", Short: "You cannot do the step; your leader is told why", RunE: func(cmd *cobra.Command, _ []string) error {
		text, err := content(cmd, e)
		if err != nil {
			return err
		}
		if text == "" {
			text = reason
		}
		return record(cmd, e, "failed", map[string]string{"reason": text})
	}}
	contentFlags(failed, "what happened, specifically")
	failed.Flags().StringVar(&reason, "reason", "", "the same as --content")
	outputFlag(failed)
	r.AddCommand(failed)
	return r
}

func recordsCmd(e *env) *cobra.Command {
	var column, kind string
	cmd := &cobra.Command{Use: "records", Short: "Print the column's live records", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := roleClient()
		if err != nil {
			return err
		}
		var recs []json.RawMessage
		q := url.Values{}
		if column != "" {
			q.Set("column", column)
		}
		if kind != "" {
			q.Set("kind", kind)
		}
		path := "/api/role/records"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		if err := c.Do(cmd.Context(), "GET", path, nil, &recs); err != nil {
			return err
		}
		if o, _ := cmd.Flags().GetString("output"); o == "json" {
			return emit(cmd, e, recs, "")
		}
		for _, r := range recs {
			fmt.Fprintln(e.out, string(r))
		}
		return nil
	}}
	cmd.Flags().StringVar(&column, "column", "", "default: your run's column")
	cmd.Flags().StringVar(&kind, "kind", "", "only records of this kind")
	outputFlag(cmd)
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
