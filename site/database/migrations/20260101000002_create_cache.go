package migrations

import (
	"time"

	"gorm.io/gorm"

	"github.com/Dshonored/ryla/migrate"
)

func init() {
	migrate.Register("20260101000002_create_cache", up20260101000002, down20260101000002)
}

// cache20260101000002 is the cache table as of this migration. It exists even
// when CACHE_DRIVER is memory, so switching drivers later needs no migration
// and no downtime.
type cache20260101000002 struct {
	Key       string     `gorm:"primaryKey;size:255"`
	Value     []byte     ``
	ExpiresAt *time.Time `gorm:"index"`
}

func (cache20260101000002) TableName() string { return "cache" }

func up20260101000002(tx *gorm.DB) error {
	return tx.Migrator().CreateTable(&cache20260101000002{})
}

func down20260101000002(tx *gorm.DB) error {
	return tx.Migrator().DropTable(&cache20260101000002{})
}
