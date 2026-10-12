package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(argv []string) error {
	if len(argv) > 0 && (argv[0] == "login" || argv[0] == "get" || argv[0] == "command" || argv[0] == "logout" || argv[0] == "request") {
		return runAPI(argv)
	}
	if len(argv) == 0 || (argv[0] != "init" && argv[0] != "login-code") {
		return errors.New("Usage: lykeion init | login-code | login | get | command | request | logout (use COMMAND --help for options)")
	}
	args := flag.NewFlagSet(argv[0], flag.ContinueOnError)
	owner := args.String("owner", "", "Owner display name")
	workspace := args.String("workspace", "", "Workspace name")
	user := args.String("user", "", "Existing installation account ID")
	path := args.String("code-file", "", "New private file for the single-use login code")
	if err := args.Parse(argv[1:]); err != nil {
		return err
	}
	if *path == "" || args.NArg() != 0 {
		return errors.New("a new code-file path is required")
	}
	if argv[0] == "init" && (strings.TrimSpace(*owner) == "" || len(*owner) > 120 || strings.TrimSpace(*workspace) == "" || len(*workspace) > 120) {
		return errors.New("owner and workspace names must contain 1–120 characters")
	}
	if argv[0] == "login-code" && *user == "" {
		return errors.New("an account ID is required")
	}
	dsn, err := store.RequiredDSN(os.Getenv("LYKEION_DATABASE_URL"))
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		return err
	}
	file, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create private code file")
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(*path)
		}
	}()
	var code, w string
	if argv[0] == "init" {
		*user, w, code, err = db.Provision(ctx, *owner, *workspace)
	} else {
		code, err = db.IssueLoginCode(ctx, *user)
	}
	if err != nil {
		return errors.New("provisioning failed")
	}
	if _, err = file.WriteString(code + "\n"); err == nil {
		err = file.Sync()
	}
	if err != nil {
		return errors.New("code file write failed; use login-code with the account ID to recover")
	}
	success = true
	fmt.Printf("Account %s", *user)
	if w != "" {
		fmt.Printf(" and workspace %s created", w)
	}
	fmt.Println(". Login code saved privately; expires in 10 minutes.")
	return nil
}
