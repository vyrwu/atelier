package main

import (
	"fmt"
	"os"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

func swapPending(slug string) string { return swapLock(slug) + ".pending" }

// A reservation bridges the gap between the launcher releasing its lock and
// the new window acquiring it. An abandoned launch expires; an active handoff
// holds the kernel lock without a time limit.
const swapLaunchTimeout = 30 * time.Second

func swapReserved(slug string) bool {
	fi, err := os.Stat(swapPending(slug))
	return err == nil && time.Since(fi.ModTime()) < swapLaunchTimeout
}

// reserveSwap owns the launch lock until its caller finishes RunAgent. An
// empty token means another launcher or handoff owns this swap already.
func reserveSwap(slug string) (string, func(), error) {
	unlock, ok, err := core.TryLockFile(swapLock(slug))
	if err != nil || !ok {
		return "", nil, err
	}
	if swapReserved(slug) {
		unlock()
		return "", nil, nil
	}
	token := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(swapPending(slug), []byte(token), 0o600); err != nil {
		unlock()
		return "", nil, err
	}
	return token, unlock, nil
}

// takeSwap waits for its launcher, checks the reservation, then keeps the lock
// through export and exec. A late child from a superseded launch cannot run.
func takeSwap(slug, token string) (func(), error) {
	var unlock func()
	var err error
	if token != "" {
		unlock, err = core.LockFile(swapLock(slug))
	} else {
		var ok bool
		unlock, ok, err = core.TryLockFile(swapLock(slug))
		if err == nil && !ok {
			return nil, fmt.Errorf("%s is already swapping", slug)
		}
	}
	if err != nil {
		return nil, err
	}
	if token == "" {
		if swapReserved(slug) {
			unlock()
			return nil, fmt.Errorf("%s is already swapping", slug)
		}
		return unlock, nil
	}
	pending, err := os.ReadFile(swapPending(slug))
	if err != nil || string(pending) != token {
		unlock()
		return nil, fmt.Errorf("%s swap reservation is no longer current", slug)
	}
	if err := os.Remove(swapPending(slug)); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}
