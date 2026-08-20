// Package seeders fills the database with data.
//
// Seeders are ordinary functions rather than a registry, because seeding order
// almost always matters and an explicit list is easier to reason about than a
// set of self-registering files.
package seeders

import (
	"context"

	"gorm.io/gorm"
)

// Run executes every seeder in order. `app db:seed` calls this.
func Run(ctx context.Context, db *gorm.DB) error {
	seeders := []func(context.Context, *gorm.DB) error{
		// seedUsers,
	}

	for _, seed := range seeders {
		if err := seed(ctx, db); err != nil {
			return err
		}
	}
	return nil
}
