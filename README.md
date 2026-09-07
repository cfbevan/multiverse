# Multiverse

Multiverse is a Go-based social platform experiment built around federation, lightweight publishing verticals, and an HTMX-first UI.
The goal is to handle multiple types of data from existing federated services.

## Overview

- Blog publishing
- Micro-blog feed
- Audio publishing
- Picture and video sections
- ActivityPub-compatible actor and inbox/outbox flows
- PostgreSQL-backed persistence
- Admin site config controls

## Tech Stack

- Go
- PostgreSQL
- HTMX
- Bootstrap
- ActivityPub primitives
- Docker-compatible local development via `mise`

## Quick start

1. Install dependencies and tools using `mise install`.
2. Start the database and app dependencies you need for local development. By copying `.env.example` to `.env` and editing as needed.
3. Run:

```bash
docker-compose -f docker-compose.yml up -d
mise run migrate
mise run serve
```

4. Open the app at the configured local port.

## Environment variables

The app reads configuration from environment variables (with defaults shown below).
These variables are used by the HTTP server in `cmd/server` and, where noted, by the admin CLI in `cmd/ctl`.

### Core runtime

| Variable     | Default      | Required          | Description                                                    |
| ------------ | ------------ | ----------------- | -------------------------------------------------------------- |
| `DEBUG`      | `false`      | No                | Enables debug mode for server runtime behavior.                |
| `PORT`       | `8080`       | No                | HTTP server port.                                              |
| `ENV`        | `dev`        | No                | Environment label (for example `dev`, `staging`, `prod`).      |
| `JWT_SECRET` | `replace-me` | Yes (practically) | Secret used to sign auth JWTs. Must be at least 16 characters. |

### Database

| Variable                  | Default                                                                      | Required | Description                                  |
| ------------------------- | ---------------------------------------------------------------------------- | -------- | -------------------------------------------- |
| `DATABASE_DSN`            | `postgres://multiverse:multiverse@localhost:5432/multiverse?sslmode=disable` | No       | PostgreSQL DSN used by server and admin CLI. |
| `DATABASE_MAX_OPEN_CONNS` | `25`                                                                         | No       | Maximum open DB connections.                 |
| `DATABASE_MAX_IDLE_CONNS` | `25`                                                                         | No       | Maximum idle DB connections.                 |
| `DATABASE_MAX_IDLE_TIME`  | `15m`                                                                        | No       | Maximum idle time for DB connections.        |

### ActivityPub

| Variable               | Default                 | Required | Description                                                                                  |
| ---------------------- | ----------------------- | -------- | -------------------------------------------------------------------------------------------- |
| `ACTIVITYPUB_BASE_URL` | `http://localhost:8080` | No       | Canonical base URL used to build ActivityPub object and actor URLs (also used by admin CLI). |
| `ACTIVITYPUB_DOMAIN`   | `localhost`             | No       | Domain used in actor identifiers and federation metadata (also used by admin CLI).           |

### Object storage (MinIO/S3-compatible)

| Variable             | Default                 | Required | Description                                              |
| -------------------- | ----------------------- | -------- | -------------------------------------------------------- |
| `STORAGE_ENDPOINT`   | `http://localhost:9000` | No       | MinIO/S3 endpoint for media storage.                     |
| `STORAGE_BUCKET`     | `multiverse`            | No       | Default bucket name when no explicit bucket is provided. |
| `STORAGE_REGION`     | `us-east-1`             | No       | Storage region used for bucket/client operations.        |
| `STORAGE_ACCESS_KEY` | `minioadmin`            | No       | Storage access key ID.                                   |
| `STORAGE_SECRET_KEY` | `minioadmin`            | No       | Storage secret access key.                               |
| `STORAGE_USE_SSL`    | `false`                 | No       | Enables TLS for storage client connections.              |

### OIDC and social sign-in

| Variable                    | Default                                       | Required               | Description                                                               |
| --------------------------- | --------------------------------------------- | ---------------------- | ------------------------------------------------------------------------- |
| `OIDC_ISSUER_URL`           | ``                                            | No                     | Generic OIDC issuer URL (optional; reserved for generic provider wiring). |
| `OIDC_CLIENT_ID`            | ``                                            | No                     | Generic OIDC client ID (optional).                                        |
| `OIDC_CLIENT_SECRET`        | ``                                            | No                     | Generic OIDC client secret (optional).                                    |
| `OIDC_REDIRECT_URL`         | `http://localhost:8080/v1/auth/oidc/callback` | No                     | Base callback URL used for OIDC provider callbacks.                       |
| `OIDC_GOOGLE_CLIENT_ID`     | ``                                            | Yes for Google login   | Google OIDC client ID used by the Google login button.                    |
| `OIDC_GOOGLE_CLIENT_SECRET` | ``                                            | Yes for Google login   | Google OIDC client secret used by the Google login button.                |
| `OIDC_APPLE_CLIENT_ID`      | ``                                            | Yes for Apple login    | Apple OIDC client ID used by the Apple login button.                      |
| `OIDC_APPLE_CLIENT_SECRET`  | ``                                            | Yes for Apple login    | Apple OIDC client secret used by the Apple login button.                  |
| `OIDC_FACEBOOK_APP_ID`      | ``                                            | Yes for Facebook login | Facebook app/client ID used by the Facebook login button.                 |
| `OIDC_FACEBOOK_APP_SECRET`  | ``                                            | Yes for Facebook login | Facebook app/client secret used by the Facebook login button.             |

Provider button visibility rules:

- Google button is shown only when both `OIDC_GOOGLE_CLIENT_ID` and `OIDC_GOOGLE_CLIENT_SECRET` are set.
- Apple button is shown only when both `OIDC_APPLE_CLIENT_ID` and `OIDC_APPLE_CLIENT_SECRET` are set.
- Facebook button is shown only when both `OIDC_FACEBOOK_APP_ID` and `OIDC_FACEBOOK_APP_SECRET` are set.
- If no provider pair is configured, OIDC provider buttons are hidden on login and signup pages.

### SMTP mailer

| Variable        | Default | Required | Description                                            |
| --------------- | ------- | -------- | ------------------------------------------------------ |
| `SMTP_HOST`     | ``      | No       | SMTP host for password reset and transactional emails. |
| `SMTP_PORT`     | `587`   | No       | SMTP port.                                             |
| `SMTP_USERNAME` | ``      | No       | SMTP auth username.                                    |
| `SMTP_PASSWORD` | ``      | No       | SMTP auth password.                                    |
| `SMTP_FROM`     | ``      | No       | From address for outbound email.                       |

### Inbox and outbox worker tuning

| Variable                  | Default   | Required | Description                                                                     |
| ------------------------- | --------- | -------- | ------------------------------------------------------------------------------- |
| `INBOX_MAX_DATE_SKEW_SEC` | `300`     | No       | Maximum accepted signature date skew (seconds) for inbound federation requests. |
| `INBOX_MAX_BODY_BYTES`    | `1048576` | No       | Maximum inbound inbox payload size in bytes.                                    |
| `INBOX_REQUIRE_HOST`      | `true`    | No       | Requires `Host` header validation for inbox requests.                           |
| `OUTBOX_POLL_SEC`         | `5`       | No       | Poll interval (seconds) for the outbox delivery worker.                         |

### Notes

- Empty-string defaults (`""`) mean the variable is optional unless that feature is enabled.
- OIDC provider buttons are rendered only when the corresponding provider credentials are configured.
- SMTP uses a no-op mailer unless both `SMTP_HOST` and `SMTP_FROM` are configured.

## Development notes

- The project uses Go modules.
- Migration files live under `migrations/`.
- UI templates live under `templates/`.
- Server and CLI entrypoints live under `cmd/`.

## License

This project is licensed under the GNU General Public License v3.0. See the [LICENSE](LICENSE) file for details.
