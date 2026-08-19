package cache

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Record is the cache table row.
type Record struct {
	Key       string     `gorm:"column:key;primaryKey;size:255"`
	Value     []byte     `gorm:"column:value"`
	ExpiresAt *time.Time `gorm:"column:expires_at;index"`
}

// TableName pins the table name.
func (Record) TableName() string { return "cache" }

// DB is a cache shared between processes.
//
// Slower than memory, and that is the trade: several instances see the same
// entries, and an invalidation on one is visible to all of them. Reach for it
// when a memory cache would give different answers depending on which instance
// answered.
type DB struct {
	DB *gorm.DB
}

// NewDB builds a database-backed store.
func NewDB(db *gorm.DB) *DB { return &DB{DB: db} }

// Get implements Store.
func (c *DB) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var rec Record
	err := c.DB.WithContext(ctx).First(&rec, "key = ?", key).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	if rec.ExpiresAt != nil && time.Now().After(*rec.ExpiresAt) {
		// Cleaned up as it is found, so expired rows do not need a sweep to
		// stay under control.
		_ = c.DB.WithContext(ctx).Delete(&Record{}, "key = ?", key).Error
		return nil, false, nil
	}
	return rec.Value, true, nil
}

// Set implements Store.
func (c *DB) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	rec := Record{Key: key, Value: value}
	if ttl > 0 {
		at := time.Now().Add(ttl)
		rec.ExpiresAt = &at
	}

	// Upsert, because writing a key that already exists is the normal case for
	// a cache, not a conflict worth reporting.
	return c.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "expires_at"}),
	}).Create(&rec).Error
}

// Delete implements Store.
func (c *DB) Delete(ctx context.Context, key string) error {
	return c.DB.WithContext(ctx).Delete(&Record{}, "key = ?", key).Error
}

// Clear implements Store.
func (c *DB) Clear(ctx context.Context) error {
	return c.DB.WithContext(ctx).Where("1 = 1").Delete(&Record{}).Error
}

// Prune deletes expired rows. Get removes them as it finds them, so this only
// matters for keys nobody asks for again.
func (c *DB) Prune(ctx context.Context) (int64, error) {
	res := c.DB.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at < ?", time.Now()).
		Delete(&Record{})
	return res.RowsAffected, res.Error
}
