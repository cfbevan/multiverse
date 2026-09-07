package models

import "time"

const (
	// VisibilityPublic allows anyone to view the content.
	VisibilityPublic = "public"
	// VisibilityUnlisted hides content from public feed listings while keeping it public.
	VisibilityUnlisted = "unlisted"
	// VisibilityFollowers restricts visibility to followers.
	VisibilityFollowers = "followers"
	// VisibilityPrivate restricts visibility to the owner only.
	VisibilityPrivate = "private"
)

// User represents a registered account.
type User struct {
	ID           int64
	Email        string
	Handle       string
	DisplayName  string
	PasswordHash string
	Bio          string
	IsAdmin      bool
	Activated    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Version      int
}

// UserSetting stores per-user UI preferences.
type UserSetting struct {
	UserID      int64
	ThemePreset string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Actor represents a local or remote federated identity.
type Actor struct {
	ID            int64
	UserID        *int64
	Handle        string
	Domain        string
	InboxURL      string
	OutboxURL     string
	FollowersURL  string
	FollowingURL  string
	PublicKeyID   string
	PublicKeyPEM  string
	PrivateKeyPEM string
	IsLocal       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BlogPost stores a long-form post.
type BlogPost struct {
	ID           int64
	ActorID      int64
	Title        string
	Slug         string
	BodyMarkdown string
	BodyHTML     string
	Visibility   string
	APObjectID   string
	PublishedAt  time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
	Version      int
}

// MicroPost stores a short social post.
type MicroPost struct {
	ID                 int64
	ActorID            int64
	Content            string
	Visibility         string
	ReplyToMicroPostID *int64
	APObjectID         string
	PublishedAt        time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
	Version            int
}

// Comment stores a reply or note on an entity.
type Comment struct {
	ID               int64
	EntityType       string
	EntityID         int64
	ActorID          int64
	Content          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
	ReplyToCommentID *int64
}

// PicturePost stores a picture content item.
type PicturePost struct {
	ID          int64
	ActorID     int64
	Caption     string
	Visibility  string
	HasPreview  bool
	APObjectID  string
	PublishedAt time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
	Version     int
	Assets      []MediaAsset
}

// VideoPost stores a video content item.
type VideoPost struct {
	ID           int64
	ActorID      int64
	Title        string
	Description  string
	Visibility   string
	MediaAssetID int64
	APObjectID   string
	PublishedAt  time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
	Version      int
}

// AudioPost stores an audio content item.
type AudioPost struct {
	ID           int64
	ActorID      int64
	Title        string
	Description  string
	Visibility   string
	MediaAssetID int64
	APObjectID   string
	PublishedAt  time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
	Version      int
}

// MediaAsset stores a media file and its metadata.
type MediaAsset struct {
	ID               int64
	OwnerActorID     int64
	Bucket           string
	ObjectKey        string
	MediaType        string
	ByteSize         int64
	SHA256Hex        string
	OriginalFilename string
	Width            *int
	Height           *int
	DurationSeconds  *int
	IsPublic         bool
	CreatedAt        time.Time
}

// InboxEvent is an inbound federated delivery event.
type InboxEvent struct {
	ID             string
	ActorID        *int64
	SourceDomain   string
	SignatureKeyID string
	Digest         string
	RequestID      string
	IdempotencyKey string
	Payload        []byte
	ReceivedAt     time.Time
	ProcessedAt    *time.Time
	Status         string
	ErrorMessage   string
}

// OutboxEvent is an outbound federated delivery event.
type OutboxEvent struct {
	ID             string
	ActorID        int64
	TargetInboxURL string
	TargetDomain   string
	IdempotencyKey string
	Payload        []byte
	Status         string
	Attempts       int
	NextAttemptAt  time.Time
	LastError      string
	SentAt         *time.Time
	CreatedAt      time.Time
}

// SiteConfig stores the enabled state of a site feature.
type SiteConfig struct {
	Key         string
	Enabled     bool
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PasswordResetToken stores a reset token and expiry metadata.
type PasswordResetToken struct {
	TokenHash string
	UserID    int64
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
