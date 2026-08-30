package core

import (
	"os"
	"syscall"
)

// flock / funlock are advisory whole-file locks (macOS + Linux, NFR-O2).
func flock(f *os.File) error   { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func funlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
