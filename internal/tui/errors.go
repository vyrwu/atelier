package tui

// ErrCancelled is returned by Run / RunPrompt / Loader-driven flows when
// the user dismisses a surface (Esc / Ctrl-C). toolmain.Finish maps it to
// exit 130 so a cross-picker chain unwinds cleanly. It is the single
// cancel sentinel across atelier (formerly internal/fzf.ErrCancelled).
type errCancelled struct{}

func (errCancelled) Error() string { return "cancelled" }

// ErrCancelled is a sentinel; compare with errors.Is.
var ErrCancelled error = errCancelled{}
