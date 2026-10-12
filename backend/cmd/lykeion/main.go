package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "init" {
		fmt.Fprintln(os.Stderr, "Usage: lykeion init --owner NAME --workspace NAME --code-file PATH")
		os.Exit(2)
	}
	args := flag.NewFlagSet("init", flag.ExitOnError)
	owner := args.String("owner", "", "Owner display name")
	workspace := args.String("workspace", "", "Workspace name")
	path := args.String("code-file", "", "New private file for the single-use login code")
	args.Parse(os.Args[2:])
	if *owner == "" || *workspace == "" || *path == "" {
		args.Usage()
		os.Exit(2)
	}
	file, e := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		fmt.Fprintln(os.Stderr, "Cannot create private code file")
		os.Exit(1)
	}
	defer file.Close()
	dsn, e := store.RequiredDSN(os.Getenv("LYKEION_DATABASE_URL"))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	db, e := store.Open(context.Background(), dsn)
	if e != nil {
		fmt.Fprintln(os.Stderr, "Database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	if e = db.Migrate(context.Background()); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	user, w, code, e := db.Provision(context.Background(), *owner, *workspace)
	if e != nil {
		fmt.Fprintln(os.Stderr, "Provision failed")
		os.Exit(1)
	}
	if _, e = file.WriteString(code + "\n"); e != nil {
		fmt.Fprintln(os.Stderr, "Code file write failed")
		os.Exit(1)
	}
	file.Sync()
	fmt.Printf("Created user %s and workspace %s. Login code saved privately; expires in 10 minutes.\n", user, w)
}
