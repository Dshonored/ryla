package migrations

import (
	"time"

	"gorm.io/gorm"

	"github.com/Dshonored/ryla/migrate"
)

func init() {
	migrate.Register("20260101000000_create_sessions", up20260101000000, down20260101000000)
}

// session20260101000000 is the shape of the sessions table as of this
// migration. It is a copy of the framework's session.Record rather than a
// reference to it, for the same reason every generated migration carries its
// own snapshot: this must keep doing what it did the day it was written, even
// after a framework upgrade changes the struct.
type session20260101000000 struct {
	ID        string    `gorm:"primaryKey;size:64"`
	Data      string    `gorm:"type:text"`
	UserID    string    `gorm:"index;size:64"`
	IP        string    `gorm:"size:45"`
	UserAgent string    `gorm:"size:255"`
	ExpiresAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

func (session20260101000000) TableName() string { return "sessions" }

func up20260101000000(tx *gorm.DB) error {
	return tx.Migrator().CreateTable(&session20260101000000{})
}

func down20260101000000(tx *gorm.DB) error {
	return tx.Migrator().DropTable(&session20260101000000{})
}
