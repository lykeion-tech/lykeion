// Command aristotle runs Aristotle Agent on this machine: it fixes issues in
// lykeion-tech/lykeion assigned to its GitHub account or labelled bug, and opens
// pull requests.
// It is an internal maintenance tool and is not shipped with lyk.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lykeion/server/internal/aristotle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "aristotle:", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := flag.String("dir", filepath.Join(home, ".lyk", "aristotle"), "private folder: state/token, mirror, run folders")
	once := flag.Bool("once", false, "work the assigned issues once and exit")
	every := flag.Duration("every", 5*time.Minute, "how often to look for assigned issues")
	timeout := flag.Duration("timeout", time.Hour, "longest a single issue may run")
	flag.Parse()

	state := filepath.Join(*dir, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	tokenPath := filepath.Join(state, "token")
	info, err := os.Stat(tokenPath)
	if err != nil {
		return fmt.Errorf("no token: put the aristotle account's fine-grained token in %s (mode 0600)", tokenPath)
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%s must be readable only by you (chmod 600)", tokenPath)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return errors.New("token file is empty")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gh := aristotle.NewGitHub(token)
	me, err := gh.Me(ctx)
	if err != nil {
		return err
	}
	a := &aristotle.Agent{Dir: *dir, GitHub: gh, Git: aristotle.Git{Home: state, Token: token}, Me: me, Log: os.Stdout, Timeout: *timeout}
	fmt.Printf("Aristotle is %s <%s>, watching %s\n", me.Login, me.Email(), aristotle.Repo)
	for {
		if err := a.Pass(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintln(os.Stderr, "pass:", err)
		}
		if *once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}
