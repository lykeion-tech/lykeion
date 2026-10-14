// Command migrate applies the schema using the owning role's connection.
package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/migrate"
	"lykeion/server/migrations"
)

func main() {
	url := os.Getenv("MIGRATE_DATABASE_URL")
	if url == "" {
		log.Fatal("MIGRATE_DATABASE_URL is required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	if err := migrate.Up(ctx, conn, migrations.FS); err != nil {
		log.Fatal(err)
	}
}
