package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/toolroute"
)

// nothingFits is what lyk tools prints when no skill or tool qualified.
const nothingFits = "Nothing in this study's tools fits that need; the researcher has been told."

func toolsCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "tools NEED", Short: "Ask which of this run's skills and connector tools fit what you need", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			door, tok := os.Getenv("LYK_DOOR"), os.Getenv("LYK_DOOR_TOKEN")
			if door == "" || tok == "" {
				return errors.New("LYK_DOOR and LYK_DOOR_TOKEN must be set: lyk tools runs inside a Lykeion agent run")
			}
			dc := &apiclient.Client{Server: strings.TrimSuffix(door, "/mcp"), Token: tok, HTTP: &http.Client{Timeout: 6 * time.Minute}}
			var res toolroute.Result
			if err := dc.Do(cmd.Context(), "POST", "/tools", map[string]string{"need": strings.Join(a, " ")}, &res); err != nil {
				return err
			}
			if o, _ := cmd.Flags().GetString("output"); o == "json" {
				return emit(cmd, e, res, "")
			}
			if res.Empty() {
				fmt.Fprintln(e.out, nothingFits)
				return nil
			}
			for _, s := range res.Skills {
				fmt.Fprintf(e.out, "Skill %s\n", s.Name)
				for _, sec := range s.Sections {
					if sec.Heading != "" {
						fmt.Fprintf(e.out, "## %s\n", sec.Heading)
					}
					fmt.Fprintln(e.out, strings.TrimRight(sec.Text, "\n"))
				}
				fmt.Fprintln(e.out)
			}
			for _, t := range res.Tools {
				fmt.Fprintf(e.out, "call_tool server %q tool %q: %s\n", t.Server, t.Tool, t.Description)
			}
			return nil
		}}
	outputFlag(c)
	return c
}
