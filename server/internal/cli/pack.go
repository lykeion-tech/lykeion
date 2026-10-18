package cli

import (
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"lykeion/server/internal/apiclient"
)

// teamCmd manages the caller's Team archives.
func teamCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "team", Short: "Team archives: upload, list versions, add; inside a run: step, done"}
	upload := &cobra.Command{Use: "upload FILE", Args: cobra.ExactArgs(1), Short: "Upload a Team archive (.zip) of your own", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		z, err := os.ReadFile(a[0])
		if err != nil {
			return err
		}
		var out struct{ ID, Name, Version, Sha256 string }
		if err := e.client().Do(cmd.Context(), "POST", "/api/packs", apiclient.Zip(z), &out); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s %s v%s %s\n", out.ID, out.Name, out.Version, out.Sha256)
		return nil
	}}
	versions := &cobra.Command{Use: "versions NAME", Args: cobra.ExactArgs(1), Short: "The versions of a Team you may add", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var rows []struct {
			ID            string `json:"id"`
			Version       string `json:"version"`
			Source        string `json:"source"`
			ApprovalsByMe bool   `json:"approvals_by_me"`
			PinnedStudies int    `json:"pinned_studies"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/packs?name="+url.QueryEscape(a[0]), nil, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			approved := "-"
			if r.ApprovalsByMe {
				approved = "approved"
			}
			fmt.Fprintf(e.out, "%s v%-8s %-9s %-9s %d studies\n", r.ID, r.Version, r.Source, approved, r.PinnedStudies)
		}
		return nil
	}}
	// Adding approves the version for the caller and gives them their own
	// copies of its agents; approve is its older name.
	add := &cobra.Command{Use: "add ID", Aliases: []string{"approve"}, Args: cobra.ExactArgs(1), Short: "Add a Team version to your studies", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/teams/"+url.PathEscape(a[0])+"/add", nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "added %s\n", a[0])
		return nil
	}}
	c.AddCommand(upload, versions, add, teamStepCmd(e), teamDoneCmd(e))
	return c
}
