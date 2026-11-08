// Package operator exposes the same space operations to the CLI, UI and Maître.
package operator

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

type want int

const (
	wantActive want = iota
	wantRetired
	wantAny
)

func target(s *core.State, slug string, state want) (core.Workspace, error) {
	if slug == core.MaitreSlug {
		return core.Workspace{}, fmt.Errorf("%q is Maître itself", slug)
	}
	w := s.Find(slug)
	if w == nil {
		return core.Workspace{}, fmt.Errorf("no space %q", slug)
	}
	if !core.ValidSpaceName(w.Slug) || !core.ValidSpaceName(w.Session) {
		return core.Workspace{}, fmt.Errorf("invalid space or session name %q", slug)
	}
	if state == wantActive && w.Retired {
		return core.Workspace{}, fmt.Errorf("%s is in the Trash; restore it first", slug)
	}
	if state == wantRetired && !w.Retired {
		return core.Workspace{}, fmt.Errorf("%s is not in the Trash", slug)
	}
	return *w, nil
}

func find(slug string, state want) (core.Workspace, error) {
	s, err := core.Read()
	if err != nil {
		return core.Workspace{}, err
	}
	return target(s, slug, state)
}

// SetRetired updates lifecycle state under the same lock as agent launch and
// purge. Restoration marks activity before making the space eligible to launch.
func SetRetired(slug string, retired bool) error {
	var actionErr error
	err := core.Update(func(s *core.State) {
		w, err := target(s, slug, wantAny)
		if err != nil {
			actionErr = err
			return
		}
		if !retired {
			if actionErr = agent.SetStatus(w.Session, core.StatusIdle); actionErr != nil {
				return
			}
		}
		current := s.Find(slug)
		current.Retired = retired
		current.RetiredAt = time.Time{}
		if retired {
			current.RetiredAt = time.Now()
		}
	})
	return errors.Join(err, actionErr)
}

func Retire(slug string) error {
	var actionErr error
	err := core.Update(func(s *core.State) {
		w, err := target(s, slug, wantActive)
		if err != nil {
			actionErr = err
			return
		}
		tmux.MoveClients(w.Session, tmux.HomeSession)
		if actionErr = tmux.StopSession(w.Session); actionErr != nil {
			return
		}
		current := s.Find(slug)
		current.Retired = true
		current.RetiredAt = time.Now()
	})
	if err == nil && actionErr == nil {
		agent.RefreshAttention()
	}
	return errors.Join(err, actionErr)
}

func Restore(slug string) error {
	if _, err := find(slug, wantRetired); err != nil {
		return err
	}
	if err := SetRetired(slug, false); err != nil {
		return err
	}
	w, err := find(slug, wantActive)
	if err != nil {
		return err
	}
	return agent.EnsureAgent(w)
}

func cleanTitle(title string) (string, error) {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || utf8.RuneCountInString(title) > 80 {
		return "", fmt.Errorf("title must be 1–80 characters")
	}
	return title, nil
}

func Rename(slug, title string) error {
	title, err := cleanTitle(title)
	if err != nil {
		return err
	}
	var w core.Workspace
	var actionErr error
	err = core.Update(func(s *core.State) {
		w, actionErr = target(s, slug, wantActive)
		if actionErr == nil {
			s.Find(slug).Title = title
			// Publish under the same lock as generated titles; otherwise an
			// older writer can overwrite the display after a later rename.
			_ = tmux.SetTitle(w.Session, title)
		}
	})
	if err = errors.Join(err, actionErr); err != nil {
		return err
	}
	// The durable title is authoritative even if the session has exited.
	_ = tmux.RefreshStatus()
	return nil
}
