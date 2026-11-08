package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

func maitreAgent(cfg core.Config) string {
	if agent.Valid(cfg.MaitreAgent) {
		return cfg.MaitreAgent
	}
	return string(agent.Of(cfg.Agent))
}

func prepareMaitre(restart bool) error {
	// Concurrent openers must share one live conversation, not respawn each other.
	unlock, err := core.LockFile(filepath.Join(core.CacheDir(), "locks", "maitre-start.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	ws, err := core.EnsureMaitre(maitreAgent(core.LoadConfig()))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ws.Root(), 0o700); err != nil {
		return err
	}
	if err := agent.LinkMaitreGuide(ws.Root()); err != nil {
		return err
	}
	if restart {
		k := agent.Of(ws.Agent)
		err = agent.RunAgent(ws, k, k.ResumeCmd("", ""))
	} else {
		err = agent.EnsureAgent(ws)
	}
	if err != nil {
		return err
	}
	if err := tmux.SetSessionOption(core.MaitreSlug, "status", "off"); err != nil {
		return err
	}
	if err := tmux.SetSessionOption(core.MaitreSlug, "key-table", "maitre"); err != nil {
		return err
	}
	return tmux.SetTitle(core.MaitreSlug, core.MaitreTitle)
}

func maitreCmd(args []string) error {
	var restart bool
	if ok, err := flags("maitre", args, func(fs *flag.FlagSet) { fs.BoolVar(&restart, "restart", false, "start a fresh Maître conversation") }); !ok {
		return err
	}
	if err := prepareMaitre(restart); err != nil {
		return err
	}
	refreshBindings()
	if restart {
		return nil
	}
	return execTmux("attach", "-t", "="+core.MaitreSlug)
}
