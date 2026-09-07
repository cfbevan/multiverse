package models

import (
	"context"
	"time"
)

// UserRepository persists user records.
type UserRepository interface {
	Insert(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByHandle(ctx context.Context, handle string) (*User, error)
	UpdatePasswordHash(ctx context.Context, userID int64, passwordHash string) error
}

// UserSettingsRepository persists user settings.
type UserSettingsRepository interface {
	GetByUserID(ctx context.Context, userID int64) (*UserSetting, error)
	UpsertThemePreset(ctx context.Context, userID int64, themePreset string) error
}

// ActorRepository persists federated actors.
type ActorRepository interface {
	InsertLocal(ctx context.Context, actor *Actor) error
	GetByID(ctx context.Context, id int64) (*Actor, error)
	GetByUserID(ctx context.Context, userID int64) (*Actor, error)
	GetByHandleAndDomain(ctx context.Context, handle, domain string) (*Actor, error)
	GetByPublicKeyID(ctx context.Context, keyID string) (*Actor, error)
	ListFollowerInboxes(ctx context.Context, actorID int64) ([]string, error)
	CountFollowers(ctx context.Context, actorID int64) (int, error)
	CountFollowing(ctx context.Context, actorID int64) (int, error)
}

// BlogPostRepository persists blog posts.
type BlogPostRepository interface {
	Insert(ctx context.Context, post *BlogPost) error
	ListPublic(ctx context.Context, limit, offset int) ([]BlogPost, error)
	ListByActor(ctx context.Context, actorID int64, limit, offset int) ([]BlogPost, error)
}

// AudioPostRepository persists audio posts.
type AudioPostRepository interface {
	Insert(ctx context.Context, post *AudioPost) error
	ListPublic(ctx context.Context, limit, offset int) ([]AudioPost, error)
	ListByActor(ctx context.Context, actorID int64, limit, offset int) ([]AudioPost, error)
}

// MicroPostRepository persists micro-posts.
type MicroPostRepository interface {
	Insert(ctx context.Context, post *MicroPost) error
	ListPublic(ctx context.Context, limit, offset int) ([]MicroPost, error)
	ListByActor(ctx context.Context, actorID int64, limit, offset int) ([]MicroPost, error)
}

// CommentRepository persists comments.
type CommentRepository interface {
	Insert(ctx context.Context, comment *Comment) error
	ListByEntity(
		ctx context.Context,
		entityType string,
		entityID int64,
		limit, offset int,
	) ([]Comment, error)
}

// InboxEventRepository persists inbound federated events.
type InboxEventRepository interface {
	Insert(ctx context.Context, event *InboxEvent) error
}

// OutboxEventRepository persists outbound federated events.
type OutboxEventRepository interface {
	Enqueue(ctx context.Context, event *OutboxEvent) error
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]OutboxEvent, error)
	MarkSent(ctx context.Context, id string, sentAt time.Time) error
	MarkFailed(ctx context.Context, id string, lastErr string, nextAttemptAt time.Time) error
}

// SiteConfigRepository persists site configuration.
type SiteConfigRepository interface {
	List(ctx context.Context) ([]SiteConfig, error)
	GetByKey(ctx context.Context, key string) (*SiteConfig, error)
	UpdateEnabled(ctx context.Context, key string, enabled bool) error
}

// PasswordResetTokenRepository persists password reset tokens.
type PasswordResetTokenRepository interface {
	Insert(ctx context.Context, token *PasswordResetToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error)
	MarkUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
}
