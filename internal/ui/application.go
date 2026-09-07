package ui

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/platform/auth"
	platformdb "github.com/cfbevan/multiverse/internal/platform/db"
	"github.com/cfbevan/multiverse/internal/platform/email"
	"github.com/cfbevan/multiverse/internal/store/postgres"
	"github.com/cfbevan/multiverse/internal/templates"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config contains the server configuration loaded from environment variables.
type Config struct {
	Debug               bool   `env:"DEBUG"                     envDefault:"false"`
	Port                int    `env:"PORT"                      envDefault:"8080"`
	Env                 string `env:"ENV"                       envDefault:"dev"`
	DatabaseDSN         string `env:"DATABASE_DSN"              envDefault:"postgres://multiverse:multiverse@localhost:5432/multiverse?sslmode=disable"`
	DatabaseMaxOpenConn int    `env:"DATABASE_MAX_OPEN_CONNS"   envDefault:"25"`
	DatabaseMaxIdleConn int    `env:"DATABASE_MAX_IDLE_CONNS"   envDefault:"25"`
	DatabaseMaxIdleTime string `env:"DATABASE_MAX_IDLE_TIME"    envDefault:"15m"`
	JWTSecret           string `env:"JWT_SECRET"                envDefault:"replace-me"`
	ActivityPubBaseURL  string `env:"ACTIVITYPUB_BASE_URL"      envDefault:"http://localhost:8080"`
	ActivityPubDomain   string `env:"ACTIVITYPUB_DOMAIN"        envDefault:"localhost"`
	StorageEndpoint     string `env:"STORAGE_ENDPOINT"          envDefault:"http://localhost:9000"`
	StorageBucket       string `env:"STORAGE_BUCKET"            envDefault:"multiverse"`
	StorageRegion       string `env:"STORAGE_REGION"            envDefault:"us-east-1"`
	StorageAccessKey    string `env:"STORAGE_ACCESS_KEY"        envDefault:"minioadmin"`
	StorageSecretKey    string `env:"STORAGE_SECRET_KEY"        envDefault:"minioadmin"`
	StorageUseSSL       bool   `env:"STORAGE_USE_SSL"           envDefault:"false"`
	OIDCIssuerURL       string `env:"OIDC_ISSUER_URL"           envDefault:""`
	OIDCClientID        string `env:"OIDC_CLIENT_ID"            envDefault:""`
	OIDCClientSecret    string `env:"OIDC_CLIENT_SECRET"        envDefault:""`
	OIDCRedirectURL     string `env:"OIDC_REDIRECT_URL"         envDefault:"http://localhost:8080/v1/auth/oidc/callback"`
	OIDCGoogleClientID  string `env:"OIDC_GOOGLE_CLIENT_ID"     envDefault:""`
	OIDCGoogleSecret    string `env:"OIDC_GOOGLE_CLIENT_SECRET" envDefault:""`
	OIDCAppleClientID   string `env:"OIDC_APPLE_CLIENT_ID"      envDefault:""`
	OIDCAppleSecret     string `env:"OIDC_APPLE_CLIENT_SECRET"  envDefault:""`
	OIDCFacebookAppID   string `env:"OIDC_FACEBOOK_APP_ID"      envDefault:""`
	OIDCFacebookSecret  string `env:"OIDC_FACEBOOK_APP_SECRET"  envDefault:""`
	SMTPHost            string `env:"SMTP_HOST"                 envDefault:""`
	SMTPPort            int    `env:"SMTP_PORT"                 envDefault:"587"`
	SMTPUsername        string `env:"SMTP_USERNAME"             envDefault:""`
	SMTPPassword        string `env:"SMTP_PASSWORD"             envDefault:""`
	SMTPFrom            string `env:"SMTP_FROM"                 envDefault:""`
	InboxMaxDateSkewSec int    `env:"INBOX_MAX_DATE_SKEW_SEC"   envDefault:"300"`
	InboxMaxBodyBytes   int64  `env:"INBOX_MAX_BODY_BYTES"      envDefault:"1048576"`
	InboxRequireHost    bool   `env:"INBOX_REQUIRE_HOST"        envDefault:"true"`
	OutboxPollSec       int    `env:"OUTBOX_POLL_SEC"           envDefault:"5"`
}

// Application stores the shared dependencies for the HTTP UI and API server.
type Application struct {
	config        Config
	logger        *slog.Logger
	tmpl          templates.TemplateCache
	db            *sql.DB
	users         models.UserRepository
	actors        models.ActorRepository
	blogPosts     models.BlogPostRepository
	audioPosts    models.AudioPostRepository
	microPosts    models.MicroPostRepository
	userSettings  models.UserSettingsRepository
	siteConfigs   models.SiteConfigRepository
	comments      models.CommentRepository
	resetTokens   models.PasswordResetTokenRepository
	inboxEvents   models.InboxEventRepository
	outboxEvents  models.OutboxEventRepository
	tokenManager  *auth.TokenManager
	mailer        email.Mailer
	storageClient *minio.Client
	httpClient    *http.Client
	wg            sync.WaitGroup
}

const (
	httpClientTimeout   = 10 * time.Second
	serverReadTimeout   = 5 * time.Second
	serverWriteTimeout  = 10 * time.Second
	shutdownWaitTimeout = 30 * time.Second
)

// NewApplication builds the application with database, templates, and auth dependencies.
func NewApplication(config Config, logger *slog.Logger) *Application {
	maxIdleTime, err := time.ParseDuration(config.DatabaseMaxIdleTime)
	if err != nil {
		logger.Error(
			"failed to parse DB idle time",
			"value",
			config.DatabaseMaxIdleTime,
			"error",
			err.Error(),
		)
		os.Exit(1)
	}

	db, err := platformdb.Open(
		config.DatabaseDSN,
		config.DatabaseMaxOpenConn,
		config.DatabaseMaxIdleConn,
		maxIdleTime,
	)
	if err != nil {
		logger.Error("failed to connect to database", "error", err.Error())
		os.Exit(1)
	}

	templateCache, err := templates.NewTemplateCache()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	tokenManager, err := auth.NewTokenManager(config.JWTSecret)
	if err != nil {
		logger.Error("invalid auth config", "error", err.Error())
		os.Exit(1)
	}

	pgModels := postgres.NewModels(db)
	var mailer email.Mailer = email.NoopMailer{}
	if config.SMTPHost != "" && config.SMTPFrom != "" {
		mailer = email.SMTPMailer{
			Host:     config.SMTPHost,
			Port:     config.SMTPPort,
			Username: config.SMTPUsername,
			Password: config.SMTPPassword,
			From:     config.SMTPFrom,
		}
	}

	storageClient, err := newStorageClient(config)
	if err != nil {
		logger.Warn("storage client unavailable", "error", err.Error())
	}

	return &Application{
		config:        config,
		logger:        logger,
		tmpl:          templateCache,
		db:            db,
		users:         pgModels.Users,
		userSettings:  pgModels.UserSettings,
		actors:        pgModels.Actors,
		blogPosts:     pgModels.BlogPosts,
		audioPosts:    pgModels.AudioPosts,
		microPosts:    pgModels.MicroPosts,
		comments:      pgModels.Comments,
		siteConfigs:   pgModels.SiteConfigs,
		resetTokens:   pgModels.PasswordResetTokens,
		inboxEvents:   pgModels.Inbox,
		outboxEvents:  pgModels.Outbox,
		tokenManager:  tokenManager,
		mailer:        mailer,
		storageClient: storageClient,
		httpClient: &http.Client{
			Timeout: httpClientTimeout,
		},
	}
}

func newStorageClient(cfg Config) (*minio.Client, error) {
	endpoint := strings.TrimSpace(cfg.StorageEndpoint)
	if endpoint == "" {
		return nil, errors.New("storage endpoint is empty")
	}

	parsed, err := url.Parse(endpoint)
	if err == nil && parsed.Host != "" {
		endpoint = parsed.Host
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: cfg.StorageUseSSL,
		Region: cfg.StorageRegion,
	})
	if err != nil {
		return nil, err
	}

	return client, nil
}

// Serve starts the HTTP server and waits for shutdown or termination signals.
func (app *Application) Serve() error {
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", app.config.Port),
		Handler:      app.Routes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
	}

	shutdownError := make(chan error)
	workerCtx, workerCancel := context.WithCancel(context.Background())

	app.wg.Go(func() {
		app.runOutboxWorker(workerCtx)
	})

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		s := <-quit

		app.logger.Info("caught signal", "signal", s.String())

		ctx, cancel := context.WithTimeout(context.Background(), shutdownWaitTimeout)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			shutdownError <- err
		}

		app.logger.Info("completing background tasks", "addr", srv.Addr)

		workerCancel()
		app.wg.Wait()
		if err := app.db.Close(); err != nil {
			shutdownError <- err

			return
		}
		shutdownError <- nil
	}()

	app.logger.Info(
		"starting server",
		"addr", srv.Addr,
		"env", app.config.Env,
		"db_open_connections", app.db.Stats().OpenConnections,
	)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownError
	if err != nil {
		return err
	}

	app.logger.Info("stopped server", "addr", srv.Addr)

	return nil
}

func (app *Application) ensureStorageBucket(ctx context.Context, bucket string) error {
	if app.storageClient == nil {
		return errors.New("object storage is not configured")
	}
	if bucket == "" {
		bucket = app.config.StorageBucket
	}

	exists, err := app.storageClient.BucketExists(ctx, bucket)
	if err == nil && exists {
		return nil
	}
	if err != nil {
		return err
	}

	if err := app.storageClient.MakeBucket(
		ctx,
		bucket,
		minio.MakeBucketOptions{Region: app.config.StorageRegion},
	); err != nil {
		if strings.Contains(err.Error(), "BucketAlreadyExists") ||
			strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
			return nil
		}

		return err
	}

	return nil
}

func (app *Application) uploadMediaFile(
	ctx context.Context,
	bucket, objectKey, mediaType string,
	contents []byte,
) error {
	if err := app.ensureStorageBucket(ctx, bucket); err != nil {
		return err
	}
	if len(contents) == 0 {
		return errors.New("upload contents are empty")
	}
	_, err := app.storageClient.PutObject(
		ctx,
		bucket,
		objectKey,
		bytes.NewReader(contents),
		int64(len(contents)),
		minio.PutObjectOptions{ContentType: mediaType},
	)

	return err
}
