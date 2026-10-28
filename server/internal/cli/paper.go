package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"lykeion/server/internal/coresearch/doc"
	"net/url"
	"strings"
	"time"
)

var paperBuildWait = 3 * time.Minute

func paperCmd(e *env) *cobra.Command {
	group := &cobra.Command{Use: "paper", Short: "Read and edit the shared Cowrite paper", PersistentPreRunE: coresearchPreRun}
	get := &cobra.Command{Use: "get PATH", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		var d doc.Doc
		parts := strings.Split(args[0], "/")
		for i := range parts {
			parts[i] = url.PathEscape(parts[i])
		}
		if err = c.Do(cmd.Context(), "GET", coresearchAPI+"/paper/"+strings.Join(parts, "/"), nil, &d); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "X-Version: %d\n", d.Version)
		_, err = fmt.Fprint(e.out, d.Text)
		return err
	}}
	var base int64
	edit := &cobra.Command{Use: "edit PATH --base N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if base < 0 {
			return errors.New("base must be nonnegative")
		}
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(e.in, 7<<20+1))
		if err != nil {
			return err
		}
		if len(data) > 7<<20 {
			return errors.New("hunks too large")
		}
		var hunks []doc.Hunk
		if err = json.Unmarshal(data, &hunks); err != nil {
			return err
		}
		if len(hunks) == 0 {
			return errors.New("hunks required")
		}
		var out struct {
			Version int64            `json:"version"`
			Results []doc.HunkResult `json:"results"`
		}
		if err = c.Do(cmd.Context(), "POST", coresearchAPI+"/paper/edit", map[string]any{"path": args[0], "base": base, "hunks": hunks}, &out); err != nil {
			return err
		}
		refused := false
		for _, r := range out.Results {
			if !r.Applied {
				refused = true
				fmt.Fprintf(e.out, "Hunk %d refused: %s\n", r.Index, r.Current)
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "X-Version: %d\n", out.Version)
		if refused {
			return coresearchStatusError(3, "one or more hunks refused")
		}
		return nil
	}}
	edit.Flags().Int64Var(&base, "base", 0, "document version to anchor against")
	_ = edit.MarkFlagRequired("base")
	build := &cobra.Command{Use: "build", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		var out struct {
			BuildID string `json:"build_id"`
		}
		if err = c.Do(cmd.Context(), "POST", coresearchAPI+"/paper/build", nil, &out); err != nil {
			return err
		}
		if out.BuildID == "" {
			return errors.New("build response has no build_id")
		}
		row := coresearchReply{ID: out.BuildID, Status: "queued"}
		if err = waitCoresearch(cmd, c, "/paper/build/"+url.PathEscape(out.BuildID), "build", paperBuildWait, &row); err != nil {
			return err
		}
		fmt.Fprintln(e.out, out.BuildID)
		return coresearchOutcome(row, "build")
	}}
	group.AddCommand(get, edit, build)
	return group
}
