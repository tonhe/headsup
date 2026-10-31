// Package changeset plans and applies edits to several managed files as one
// unit: every file is validated and diffed first, then written in order; if
// a write fails, the files already written are rolled back so the host
// never keeps half a change.
package changeset

import (
	"context"
	"errors"
	"fmt"

	"github.com/tonhe/headsup/internal/host"
)

// Item is one file's proposed content.
type Item struct {
	File *host.File
	Next []byte
}

// Plan validates and diffs every item. Items whose content is unchanged
// (and that are not new files) are skipped.
func Plan(ctx context.Context, h host.Host, items []Item) ([]*host.Change, error) {
	var out []*host.Change
	for _, it := range items {
		if it.File == nil {
			continue
		}
		if !it.File.New && string(it.File.Raw) == string(it.Next) {
			continue
		}
		c, err := h.Plan(ctx, it.File, it.Next)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.File.Path, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// Valid reports whether every change is free of error-severity issues.
func Valid(cs []*host.Change) bool {
	for _, c := range cs {
		if !c.Valid() {
			return false
		}
	}
	return true
}

// Effect is the latest point at which the whole set takes effect.
func Effect(cs []*host.Change) host.Effect {
	e := host.EffectLive
	for _, c := range cs {
		e = max(e, c.Effect)
	}
	return e
}

// Danger is the highest danger of any change.
func Danger(cs []*host.Change) host.Danger {
	d := host.DangerNone
	for _, c := range cs {
		d = max(d, c.Danger)
	}
	return d
}

// Apply writes each change in order. On failure it rolls back what it
// already wrote (newest first) and returns the write error, joined with any
// rollback error.
func Apply(ctx context.Context, h host.Host, cs []*host.Change) ([]host.Applied, error) {
	var done []host.Applied
	for _, c := range cs {
		a, err := h.Apply(ctx, c)
		if err != nil {
			err = fmt.Errorf("%s: %w", c.File.Path, err)
			if rerr := Rollback(context.WithoutCancel(ctx), h, done); rerr != nil {
				err = errors.Join(err, fmt.Errorf("undoing the files already written: %w", rerr))
			}
			return nil, err
		}
		done = append(done, a)
	}
	return done, nil
}

// Rollback restores applied writes, newest first, and reports every failure.
func Rollback(ctx context.Context, h host.Host, as []host.Applied) error {
	var errs []error
	for i := len(as) - 1; i >= 0; i-- {
		if err := h.Rollback(ctx, as[i]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", as[i].File.Path, err))
		}
	}
	return errors.Join(errs...)
}
