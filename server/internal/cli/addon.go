package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func addonCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "addon", Short: "The workspace's additions to a column's stage roles; only the owner changes them"}
	show := &cobra.Command{Use: "show [COLUMN]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var list []struct {
			BoardColumn string          `json:"board_column"`
			SkillMD     string          `json:"skill_md"`
			MCP         json.RawMessage `json:"mcp"`
			AddonHash   string          `json:"addon_hash"`
			Invalid     string          `json:"invalid"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/workspaces/"+e.cfg.Workspace+"/addons", nil, &list); err != nil {
			return err
		}
		for _, x := range list {
			if len(a) == 1 && x.BoardColumn != a[0] {
				continue
			}
			fmt.Fprintf(e.out, "== %s %s\n", x.BoardColumn, x.AddonHash)
			if x.Invalid != "" {
				fmt.Fprintf(e.out, "INVALID: %s\n", x.Invalid)
			}
			fmt.Fprintf(e.out, "mcp: %s\n", x.MCP)
			if x.SkillMD != "" {
				fmt.Fprintln(e.out, x.SkillMD)
			}
		}
		return nil
	}}

	var skillFile, mcpFile string
	var clear bool
	read := func(p string) ([]byte, error) {
		if p == "-" {
			return io.ReadAll(e.in)
		}
		return os.ReadFile(p)
	}
	set := &cobra.Command{Use: "set COLUMN", Args: cobra.ExactArgs(1), Short: "Change a column's add-on", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		body := map[string]any{}
		if clear {
			body["skill_md"], body["mcp"] = "", []any{}
		}
		if skillFile != "" {
			b, err := read(skillFile)
			if err != nil {
				return err
			}
			if !utf8.Valid(b) {
				return fmt.Errorf("%s is not valid UTF-8", skillFile)
			}
			body["skill_md"] = string(b)
		}
		if mcpFile != "" {
			b, err := read(mcpFile)
			if err != nil {
				return err
			}
			if !json.Valid(b) {
				return fmt.Errorf("%s is not JSON", mcpFile)
			}
			body["mcp"] = json.RawMessage(b)
		}
		if len(body) == 0 {
			return errors.New("nothing to set: give --skill, --mcp or --clear")
		}
		var out struct {
			AddonHash string `json:"addon_hash"`
		}
		if err := e.client().Do(cmd.Context(), "PATCH", "/api/workspaces/"+e.cfg.Workspace+"/addons/"+a[0], body, &out); err != nil {
			return err
		}
		if out.AddonHash == "" {
			out.AddonHash = "(none)"
		}
		fmt.Fprintf(e.out, "%s add-on saved: %s\n", a[0], out.AddonHash)
		return nil
	}}
	set.Flags().StringVar(&skillFile, "skill", "", "file with the text added to the column's roles; - reads stdin")
	set.Flags().StringVar(&mcpFile, "mcp", "", `file with a JSON array of {"name","url"} tool servers; - reads stdin`)
	set.Flags().BoolVar(&clear, "clear", false, "remove the text and the servers")
	c.AddCommand(show, set)
	return c
}
