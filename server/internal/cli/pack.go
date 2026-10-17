package cli

import (
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"lykeion/server/internal/apiclient"
)

// teamCmd manages the caller's Team: the field pack their new studies use.
func teamCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "team", Short: "Your Team: the field pack your new studies use"}
	list := &cobra.Command{Use: "list", Short: "Teams Lykeion ships and the ones you uploaded", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var rows []struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Source      string   `json:"source"`
			Description string   `json:"description"`
			StartKinds  []string `json:"start_kinds"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/packs/catalog", nil, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			fmt.Fprintf(e.out, "%-20s v%-8s %-9s %v %s\n", r.Name, r.Version, r.Source, r.StartKinds, r.Description)
		}
		return nil
	}}
	use := &cobra.Command{Use: "use NAME", Args: cobra.ExactArgs(1), Short: "Make NAME your Team", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		if err := e.client().Do(cmd.Context(), "PUT", "/api/me/pack", map[string]string{"name": a[0]}, nil); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "Team: %s\n", a[0])
		return nil
	}}
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
	versions := &cobra.Command{Use: "versions NAME", Args: cobra.ExactArgs(1), Short: "The versions of a Team you may approve", RunE: func(cmd *cobra.Command, a []string) error {
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
	approve := &cobra.Command{Use: "approve ID", Args: cobra.ExactArgs(1), Short: "Approve a Team version for your studies", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/packs/"+url.PathEscape(a[0])+"/approve", nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "approved %s\n", a[0])
		return nil
	}}
	c.AddCommand(list, use, upload, versions, approve)
	return c
}
