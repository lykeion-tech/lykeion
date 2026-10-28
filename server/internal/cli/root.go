package cli

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"lykeion/server/internal/apiclient"
)

// env bundles what every command needs: config, its path, a client, and the
// streams to read prompts from and write results to.
type env struct {
	cfg         Config
	path        string
	in          io.Reader
	out         io.Writer
	openBrowser func(string) error
}

func (e *env) client() *apiclient.Client {
	return &apiclient.Client{Server: e.cfg.Server, Token: e.cfg.Token, Workspace: e.cfg.Workspace}
}

func (e *env) signedIn() error {
	if e.cfg.Token == "" || e.cfg.Server == "" {
		return errors.New("not signed in: run lyk login")
	}
	return nil
}

func (e *env) inWorkspace() error {
	if err := e.signedIn(); err != nil {
		return err
	}
	if e.cfg.Workspace == "" {
		return errors.New("no workspace selected: run lyk workspace switch <slug>")
	}
	return nil
}

func Root(in io.Reader, out io.Writer) *cobra.Command {
	return rootWithBrowser(in, out, openBrowser)
}

func rootWithBrowser(in io.Reader, out io.Writer, opener func(string) error) *cobra.Command {
	e := &env{in: in, out: out, openBrowser: opener}
	root := &cobra.Command{
		Use:           "lyk",
		Short:         "Lykeion: research questions carried across a board by agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			var err error
			e.cfg, e.path, err = LoadConfig()
			return err
		},
	}
	root.SetOut(out)
	root.AddCommand(loginCmd(e), logoutCmd(e), whoamiCmd(e), workspaceCmd(e), cardCmd(e), inboxCmd(e), daemonCmd(e), runtimeCmd(e), resultCmd(e), recordsCmd(e), blobCmd(e), boardCmd(e), addonCmd(e), autoresearchCmd(e), reportCmd(e), openCardCmd(e), teamCmd(e), recordCmd(e), callsCmd(e), runsCmd(e), toolsCmd(e), outputCmd(e), cellCmd(e), coresearchRunCmd(e), coresearchFileCmd(e), coresearchEnvCmd(e), paperCmd(e))
	return root
}
