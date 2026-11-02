package app

import (
	"context"
	"errors"

	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
)

// Applier adapts a hotreload.Reloader to web.ConfigApplier, so dashboard
// saves take the same validate/prepare/commit/rollback path as file edits.
type Applier struct{ R *hotreload.Reloader }

// ApplyConfig applies next; an unchanged config is not an error.
func (a Applier) ApplyConfig(ctx context.Context, next *config.Config) error {
	_, err := a.R.Apply(ctx, next)
	if errors.Is(err, hotreload.ErrNoChange) {
		return nil
	}
	return err
}
