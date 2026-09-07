package admincli

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/caarlos0/env"
	"github.com/cfbevan/multiverse/internal/models"
	ap "github.com/cfbevan/multiverse/internal/platform/activitypub"
	platformdb "github.com/cfbevan/multiverse/internal/platform/db"
	pq "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// Config holds the database and ActivityPub settings for the admin CLI.
type Config struct {
	DatabaseDSN         string `env:"DATABASE_DSN"            envDefault:"postgres://multiverse:multiverse@localhost:5432/multiverse?sslmode=disable"`
	DatabaseMaxOpenConn int    `env:"DATABASE_MAX_OPEN_CONNS" envDefault:"25"`
	DatabaseMaxIdleConn int    `env:"DATABASE_MAX_IDLE_CONNS" envDefault:"25"`
	DatabaseMaxIdleTime string `env:"DATABASE_MAX_IDLE_TIME"  envDefault:"15m"`
	ActivityPubBaseURL  string `env:"ACTIVITYPUB_BASE_URL"    envDefault:"http://localhost:8080"`
	ActivityPubDomain   string `env:"ACTIVITYPUB_DOMAIN"      envDefault:"localhost"`
}

type migration struct {
	name string
	body string
}

// LoadConfigFromEnv reads the admin CLI configuration from environment variables.
func LoadConfigFromEnv() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

const (
	defaultTimeout        = 5 * time.Second
	generatedPasswordLen  = 18
	minimumGeneratedChars = 12
	tabWriterPadding      = 2
	fileSizeThreshold     = 1024
)

// Execute runs the selected admin CLI command.
func Execute(cfg Config, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)

		return flag.ErrHelp
	}

	switch args[0] {
	case "migrate":
		return runMigrate(cfg, args[1:], stdout, stderr)
	case "add-user":
		return runAddUser(cfg, args[1:], stdout, stderr)
	case "info":
		return runInfo(cfg, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)

		return nil
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)

		return flag.ErrHelp
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "ctl is an admin helper for multiverse")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  ctl migrate [flags]")
	_, _ = fmt.Fprintln(w, "  ctl add-user [flags]")
	_, _ = fmt.Fprintln(w, "  ctl info")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  migrate   run SQL migrations from the migrations directory")
	_, _ = fmt.Fprintln(w, "  add-user  create a user and local actor directly in the database")
	_, _ = fmt.Fprintln(w, "  info      print database and content statistics")
}

func runMigrate(cfg Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", cfg.DatabaseDSN, "PostgreSQL DSN")
	dir := fs.String("dir", "migrations", "directory containing SQL migration files")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	db, err := openDB(*dsn, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	migrations, err := loadMigrations(*dir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		_, _ = fmt.Fprintln(stdout, "no migrations found")

		return nil
	}

	applied := 0
	ctx := context.Background()
	for _, m := range migrations {
		hasRun, err := migrationApplied(ctx, db, m.name)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", m.name, err)
		}
		if hasRun {
			continue
		}

		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}

		applied++
		_, _ = fmt.Fprintf(stdout, "applied migration: %s\n", m.name)
	}

	_, _ = fmt.Fprintf(stdout, "migration run complete, %d applied\n", applied)

	return nil
}

func runAddUser(cfg Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("add-user", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "user email address")
	handle := fs.String("handle", "", "user handle")
	displayName := fs.String("display-name", "", "display name")
	password := fs.String("password", "", "password (generated if omitted)")
	bio := fs.String("bio", "", "profile bio")
	admin := fs.Bool("admin", false, "make the user an admin")
	activated := fs.Bool("activated", true, "mark the user as activated")
	baseURL := fs.String("base-url", cfg.ActivityPubBaseURL, "base URL for local actor endpoints")
	domain := fs.String("domain", cfg.ActivityPubDomain, "ActivityPub domain")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if strings.TrimSpace(*email) == "" || strings.TrimSpace(*handle) == "" ||
		strings.TrimSpace(*displayName) == "" {
		return errors.New("email, handle, and display-name are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	plaintextPassword := strings.TrimSpace(*password)
	generatedPassword := false
	if plaintextPassword == "" {
		plaintextPassword = generatePassword(generatedPasswordLen)
		generatedPassword = true
	}

	hash, err := hashPassword(plaintextPassword)
	if err != nil {
		return err
	}

	db, err := openDB(cfg.DatabaseDSN, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	user := models.User{
		Email:        strings.ToLower(strings.TrimSpace(*email)),
		Handle:       strings.TrimSpace(*handle),
		DisplayName:  strings.TrimSpace(*displayName),
		PasswordHash: hash,
		Bio:          strings.TrimSpace(*bio),
		IsAdmin:      *admin,
		Activated:    *activated,
	}

	if err := tx.QueryRowContext(
		ctx,
		`INSERT INTO users (email, handle, display_name, password_hash, bio, is_admin, activated) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at, updated_at, version`,
		user.Email,
		user.Handle,
		user.DisplayName,
		user.PasswordHash,
		user.Bio,
		user.IsAdmin,
		user.Activated,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt, &user.Version); err != nil {
		return formatConstraintError(err)
	}

	base := strings.TrimSuffix(strings.TrimSpace(*baseURL), "/")
	publicKeyPEM, privateKeyPEM, err := ap.GenerateActorKeyPair()
	if err != nil {
		return err
	}

	actor := models.Actor{
		UserID:        &user.ID,
		Handle:        user.Handle,
		Domain:        strings.TrimSpace(*domain),
		InboxURL:      base + "/inbox",
		OutboxURL:     base + "/actors/" + user.Handle + "/outbox",
		FollowersURL:  base + "/actors/" + user.Handle + "/followers",
		FollowingURL:  base + "/actors/" + user.Handle + "/following",
		PublicKeyID:   base + "/actors/" + user.Handle + "#main-key",
		PublicKeyPEM:  publicKeyPEM,
		PrivateKeyPEM: privateKeyPEM,
		IsLocal:       true,
	}

	if err := tx.QueryRowContext(
		ctx,
		`INSERT INTO actors (user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true) RETURNING id, created_at, updated_at`,
		actor.UserID,
		actor.Handle,
		actor.Domain,
		actor.InboxURL,
		actor.OutboxURL,
		actor.FollowersURL,
		actor.FollowingURL,
		actor.PublicKeyID,
		actor.PublicKeyPEM,
		actor.PrivateKeyPEM,
	).Scan(&actor.ID, &actor.CreatedAt, &actor.UpdatedAt); err != nil {
		return formatConstraintError(err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "created user %s (%s)\n", user.Handle, user.Email)
	_, _ = fmt.Fprintf(stdout, "user id: %d\n", user.ID)
	_, _ = fmt.Fprintf(stdout, "actor id: %d\n", actor.ID)
	if generatedPassword {
		_, _ = fmt.Fprintf(stdout, "generated password: %s\n", plaintextPassword)
	}

	return nil
}

func runInfo(cfg Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	db, err := openDB(cfg.DatabaseDSN, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	type infoRow struct {
		DatabaseName  string
		Migrations    int64
		Users         int64
		ActiveUsers   int64
		AdminUsers    int64
		LocalActors   int64
		RemoteActors  int64
		Follows       int64
		BlogPosts     int64
		MicroPosts    int64
		AudioPosts    int64
		PicturePosts  int64
		VideoPosts    int64
		MediaAssets   int64
		MediaBytes    int64
		DatabaseBytes int64
	}

	query := `
SELECT
	current_database(),
	CASE WHEN to_regclass('schema_migrations') IS NULL THEN 0 ELSE (SELECT COUNT(*) FROM schema_migrations) END,
	(SELECT COUNT(*) FROM users),
	(SELECT COUNT(*) FROM users WHERE activated),
	(SELECT COUNT(*) FROM users WHERE is_admin),
	(SELECT COUNT(*) FROM actors WHERE is_local),
	(SELECT COUNT(*) FROM actors WHERE NOT is_local),
	(SELECT COUNT(*) FROM follows),
	(SELECT COUNT(*) FROM blog_posts WHERE deleted_at IS NULL),
	(SELECT COUNT(*) FROM micro_posts WHERE deleted_at IS NULL),
	(SELECT COUNT(*) FROM audio_posts WHERE deleted_at IS NULL),
	(SELECT COUNT(*) FROM picture_posts WHERE deleted_at IS NULL),
	(SELECT COUNT(*) FROM video_posts WHERE deleted_at IS NULL),
	(SELECT COUNT(*) FROM media_assets),
	COALESCE((SELECT SUM(byte_size) FROM media_assets), 0),
	pg_database_size(current_database())
`

	var info infoRow
	if err := db.QueryRowContext(ctx, query).Scan(
		&info.DatabaseName,
		&info.Migrations,
		&info.Users,
		&info.ActiveUsers,
		&info.AdminUsers,
		&info.LocalActors,
		&info.RemoteActors,
		&info.Follows,
		&info.BlogPosts,
		&info.MicroPosts,
		&info.AudioPosts,
		&info.PicturePosts,
		&info.VideoPosts,
		&info.MediaAssets,
		&info.MediaBytes,
		&info.DatabaseBytes,
	); err != nil {
		return err
	}

	entries := []struct {
		label string
		value string
	}{
		{"database", info.DatabaseName},
		{"migrations applied", strconv.FormatInt(info.Migrations, 10)},
		{
			"users",
			fmt.Sprintf(
				"%d total / %d active / %d admin",
				info.Users,
				info.ActiveUsers,
				info.AdminUsers,
			),
		},
		{"actors", fmt.Sprintf("%d local / %d remote", info.LocalActors, info.RemoteActors)},
		{"follows", strconv.FormatInt(info.Follows, 10)},
		{"blog posts", strconv.FormatInt(info.BlogPosts, 10)},
		{"micro posts", strconv.FormatInt(info.MicroPosts, 10)},
		{"audio posts", strconv.FormatInt(info.AudioPosts, 10)},
		{"picture posts", strconv.FormatInt(info.PicturePosts, 10)},
		{"video posts", strconv.FormatInt(info.VideoPosts, 10)},
		{"media assets", strconv.FormatInt(info.MediaAssets, 10)},
		{
			"media storage",
			fmt.Sprintf("%s (%d bytes)", formatBytes(info.MediaBytes), info.MediaBytes),
		},
		{
			"database size",
			fmt.Sprintf("%s (%d bytes)", formatBytes(info.DatabaseBytes), info.DatabaseBytes),
		},
	}

	width := tabwriter.NewWriter(stdout, 0, 0, tabWriterPadding, ' ', 0)
	_, _ = fmt.Fprintln(width, "Metric\tValue")
	_, _ = fmt.Fprintln(width, "------\t-----")
	for _, entry := range entries {
		_, _ = fmt.Fprintf(width, "%s\t%s\n", entry.label, entry.value)
	}

	return width.Flush()
}

func openDB(dsn string, cfg Config) (*sql.DB, error) {
	if dsn == "" {
		dsn = cfg.DatabaseDSN
	}

	maxIdleTime, err := time.ParseDuration(cfg.DatabaseMaxIdleTime)
	if err != nil {
		return nil, err
	}

	return platformdb.Open(dsn, cfg.DatabaseMaxOpenConn, cfg.DatabaseMaxIdleConn, maxIdleTime)
}

func ensureMigrationsTable(db *sql.DB) error {
	const stmt = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);`
	_, err := db.Exec(stmt)

	return err
}

func loadMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	dirFS := os.DirFS(dir)

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		body, err := fs.ReadFile(dirFS, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, name), err)
		}

		migrations = append(migrations, migration{name: name, body: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].name < migrations[j].name
	})

	return migrations, nil
}

func migrationApplied(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var exists bool
	const query = `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`
	err := db.QueryRowContext(ctx, query, name).Scan(&exists)

	return exists, err
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.body); err != nil {
		return err
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES ($1, $2)`,
		m.name,
		time.Now().UTC(),
	); err != nil {
		return err
	}

	return tx.Commit()
}

func hashPassword(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(hash), nil
}

func generatePassword(length int) string {
	if length < minimumGeneratedChars {
		length = minimumGeneratedChars
	}

	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "multiverse-admin"
	}

	for i := range bytes {
		bytes[i] = alphabet[int(bytes[i])%len(alphabet)]
	}

	return string(bytes)
}

func formatConstraintError(err error) error {
	if pqErr, ok := errors.AsType[*pq.Error](err); ok {
		switch pqErr.Constraint {
		case "users_email_key":
			return fmt.Errorf("user email already exists: %w", err)
		case "users_handle_key":
			return fmt.Errorf("user handle already exists: %w", err)
		case "actors_handle_key", "actors_handle_domain_key":
			return fmt.Errorf("actor already exists for that handle/domain: %w", err)
		}
	}

	return err
}

func formatBytes(n int64) string {
	if n < fileSizeThreshold {
		return fmt.Sprintf("%d B", n)
	}

	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(n)
	unit := "B"
	for _, next := range units {
		value /= fileSizeThreshold
		unit = next
		if value < fileSizeThreshold {
			break
		}
	}

	return fmt.Sprintf("%.1f %s", value, unit)
}
