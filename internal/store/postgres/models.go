package postgres

import "database/sql"

// Models bundles all PostgreSQL-backed data models for the app.
type Models struct {
	Users               UserModel
	UserSettings        UserSettingsModel
	Actors              ActorModel
	BlogPosts           BlogPostModel
	AudioPosts          AudioPostModel
	MicroPosts          MicroPostModel
	Comments            CommentModel
	Inbox               InboxEventModel
	Outbox              OutboxEventModel
	SiteConfigs         SiteConfigModel
	PasswordResetTokens PasswordResetTokenModel
}

// NewModels constructs the model bundle for a database connection.
func NewModels(db *sql.DB) Models {
	return Models{
		Users:               UserModel{DB: db},
		UserSettings:        UserSettingsModel{DB: db},
		Actors:              ActorModel{DB: db},
		BlogPosts:           BlogPostModel{DB: db},
		AudioPosts:          AudioPostModel{DB: db},
		MicroPosts:          MicroPostModel{DB: db},
		Comments:            CommentModel{DB: db},
		Inbox:               InboxEventModel{DB: db},
		Outbox:              OutboxEventModel{DB: db},
		SiteConfigs:         SiteConfigModel{DB: db},
		PasswordResetTokens: PasswordResetTokenModel{DB: db},
	}
}
