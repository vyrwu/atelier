package core

import (
	"os"
	"syscall"
)

// flock / funlock are advisory whole-file locks (macOS + Linux, NFR-O2).
func flock(f *os.File) error   { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func funlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// tryFlock takes the lock only if it is free, failing at once otherwise.
func tryFlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
