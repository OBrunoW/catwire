package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/OBrunoW/catwire/internal/domain"
)

type Cleaner struct {
	unit    UnitOfWork
	storage Storage
}

func NewCleaner(unit UnitOfWork, storage Storage) *Cleaner {
	return &Cleaner{unit: unit, storage: storage}
}

func (c *Cleaner) RunOnce(ctx context.Context, limit int) (int, error) {
	expired, err := c.listExpired(ctx, limit)
	if err != nil {
		return 0, err
	}
	var removed int
	var failures []error
	for _, item := range expired {
		if err := c.remove(ctx, item); err != nil {
			failures = append(failures, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}

func (c *Cleaner) listExpired(ctx context.Context, limit int) ([]domain.Media, error) {
	var expired []domain.Media
	err := c.unit.Do(ctx, func(unit Unit) error {
		items, err := unit.Media.ListExpiredUnattached(ctx, limit)
		expired = items
		return err
	})
	return expired, err
}

func (c *Cleaner) remove(ctx context.Context, item domain.Media) error {
	if item.StorageKey != nil {
		if err := c.storage.Delete(ctx, *item.StorageKey); err != nil {
			return fmt.Errorf("media: delete object %s: %w", item.ID, err)
		}
	}
	return c.unit.Do(ctx, func(unit Unit) error {
		return unit.Media.Delete(ctx, item.ID)
	})
}
