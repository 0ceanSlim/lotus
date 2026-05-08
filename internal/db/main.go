package db

import (
	"database/sql"
	"embed"

	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations
var migrationsFS embed.FS

func NewDB(path string) (*sql.DB, error) {
	dbi, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	migrations := &migrate.EmbedFileSystemMigrationSource{
		FileSystem: migrationsFS,
		Root:       "migrations",
	}
	_, err = migrate.Exec(dbi, "sqlite3", migrations, migrate.Up)
	if err != nil {
		return nil, err
	}

	return dbi, nil
}
