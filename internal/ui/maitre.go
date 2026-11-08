package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

type maitreOutput []byte
type maitreExit struct{ err error }

type maitreView struct {
	w, h  int
	term  *vt.Emulator
	tty   *os.File
	child *exec.Cmd
	ctx   context.Context
	err   error
}

// RunMaitre embeds the attached native terminal in the same Bubble Tea frame
// as the other screens. Closing the PTY detaches only this client; the agent's
// session and conversation remain alive. Native menus still receive Escape
// before the maitre key table does, so canceling a menu does not close Maître.
func RunMaitre() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &maitreView{ctx: ctx}
	defer func() {
		if m.tty != nil {
			_ = m.tty.Close()
		}
		if m.term != nil {
			closeTerminal(m.term)
		}
	}()
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		return err
	}
	return m.err
}

func (m *maitreView) Init() tea.Cmd { return nil }

func maitreFooter() string {
	return shortcutFooter("M-r restart", "M-x swap agent", "esc close")
}

func (m *maitreView) size() (int, int) {
	w := max(1, m.w-4)
	footer := lipgloss.NewStyle().Width(w).Render(maitreFooter())
	return w, max(1, m.h-5-lipgloss.Height(footer))
}

func (m *maitreView) read() tea.Msg {
	b := make([]byte, 32768)
	n, _ := m.tty.Read(b)
	if n > 0 {
		return maitreOutput(b[:n])
	}
	// The child's wait command owns the exit status (a PTY commonly ends in EIO).
	return nil
}

func (m *maitreView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		w, h := m.size()
		if m.term == nil {
			m.term = vt.NewEmulator(w, h)
			// #nosec G204 G702 -- Pinned tmux executable and fixed session/arguments.
			m.child = exec.CommandContext(m.ctx, tmux.Binary(), "-L", core.LoadConfig().Socket, "attach", "-t", "="+core.MaitreSlug)
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") && !strings.HasPrefix(e, "TERM=") {
					m.child.Env = append(m.child.Env, e)
				}
			}
			m.child.Env = append(m.child.Env, "TERM=xterm-256color")
			var err error
			m.tty, err = pty.StartWithSize(m.child, terminalSize(w, h))
			if err != nil {
				m.err = err
				return m, tea.Quit
			}
			// Terminal replies and input share one ordered stream to the native client.
			go func() { _, _ = io.Copy(m.tty, m.term) }()
			return m, tea.Batch(m.read, func() tea.Msg { return maitreExit{m.child.Wait()} })
		}
		m.term.Resize(w, h)
		if err := pty.Setsize(m.tty, terminalSize(w, h)); err != nil {
			m.err = err
			return m, tea.Quit
		}
	case maitreOutput:
		_, _ = m.term.Write(msg)
		return m, m.read
	case maitreExit:
		m.err = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		if m.term != nil {
			sendTerminalKey(m.term, msg)
		}
	case tea.MouseMsg:
		if m.term != nil {
			m.mouse(msg)
		}
	}
	return m, nil
}

func terminalSize(w, h int) *pty.Winsize {
	// #nosec G115 -- Both dimensions are clamped to the uint16 range before conversion.
	return &pty.Winsize{Cols: uint16(min(max(w, 1), 65535)), Rows: uint16(min(max(h, 1), 65535))}
}

func (m *maitreView) View() string {
	if m.w < 4 || m.h < 8 {
		return ""
	}
	body := ""
	if m.term != nil {
		// Bubble Tea owns the host cursor. Show the native cursor as a styled cell
		// within the terminal viewport, never by moving it outside the frame.
		pos := m.term.CursorPosition()
		cell := m.term.CellAt(pos.X, pos.Y)
		if cell != nil && !m.term.CursorHidden() {
			cell = cell.Clone()
			marked := cell.Clone()
			marked.Style.Attrs ^= uv.AttrReverse
			m.term.SetCell(pos.X, pos.Y, marked)
			body = m.term.Render()
			m.term.SetCell(pos.X, pos.Y, cell)
		} else {
			body = m.term.Render()
		}
	}
	return overlayFrame(m.w, m.h, titleStyle.Render("Maître")+"\n\n"+body, maitreFooter())
}

func sendTerminalKey(term *vt.Emulator, msg tea.KeyMsg) {
	if msg.Paste {
		term.Paste(string(msg.Runes))
		return
	}
	mod := uv.KeyMod(0)
	if msg.Alt {
		mod |= uv.ModAlt
	}
	if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
		for _, r := range msg.Runes {
			term.SendKey(uv.KeyPressEvent{Code: r, Text: string(r), Mod: mod})
		}
		return
	}
	// Control bytes retain their exact meaning, including Enter, Tab and Escape.
	if msg.Type >= 0 && msg.Type <= 127 {
		s := string(rune(msg.Type))
		if msg.Alt {
			s = "\x1b" + s
		}
		_, _ = io.WriteString(term.InputPipe(), s)
		return
	}
	name := msg.Type.String()
	for {
		prefix, rest, ok := strings.Cut(name, "+")
		if !ok {
			break
		}
		switch prefix {
		case "ctrl":
			mod |= uv.ModCtrl
		case "shift":
			mod |= uv.ModShift
		case "alt":
			mod |= uv.ModAlt
		}
		name = rest
	}
	codes := map[string]rune{"up": uv.KeyUp, "down": uv.KeyDown, "left": uv.KeyLeft, "right": uv.KeyRight,
		"home": uv.KeyHome, "end": uv.KeyEnd, "pgup": uv.KeyPgUp, "pgdown": uv.KeyPgDown,
		"insert": uv.KeyInsert, "delete": uv.KeyDelete, "tab": uv.KeyTab}
	code := codes[name]
	if msg.Type == tea.KeyShiftTab {
		code, mod = uv.KeyTab, mod|uv.ModShift
	}

	// x/vt handles terminal modes for plain keys, but modified navigation keys
	// need their standard xterm CSI encoding.
	suffix := map[string]string{"up": "A", "down": "B", "right": "C", "left": "D", "home": "H", "end": "F"}[name]
	number := map[string]int{"insert": 2, "delete": 3, "pgup": 5, "pgdown": 6}[name]
	if n, err := strconv.Atoi(strings.TrimPrefix(name, "f")); strings.HasPrefix(name, "f") && err == nil && n >= 1 && n <= 20 {
		code = uv.KeyF1 + rune(n-1)
		numbers := []int{11, 12, 13, 14, 15, 17, 18, 19, 20, 21, 23, 24, 25, 26, 28, 29, 31, 32, 33, 34}
		number = numbers[n-1]
		if n > 12 && mod == 0 {
			_, _ = fmt.Fprintf(term.InputPipe(), "\x1b[%d~", number)
			return
		}
	}
	if mod != 0 && (suffix != "" || number != 0) {
		parameter := 1
		if mod&uv.ModShift != 0 {
			parameter += 1
		}
		if mod&uv.ModAlt != 0 {
			parameter += 2
		}
		if mod&uv.ModCtrl != 0 {
			parameter += 4
		}
		if suffix != "" {
			_, _ = fmt.Fprintf(term.InputPipe(), "\x1b[1;%d%s", parameter, suffix)
		} else {
			_, _ = fmt.Fprintf(term.InputPipe(), "\x1b[%d;%d~", number, parameter)
		}
	} else if code != 0 {
		term.SendKey(uv.KeyPressEvent{Code: code, Mod: mod})
	}

}

func (m *maitreView) mouse(msg tea.MouseMsg) {
	x, y := msg.X-2, msg.Y-3 // frame padding and title
	w, h := m.size()
	if x < 0 || y < 0 || x >= w || y >= h {
		return
	}
	if msg.Button < tea.MouseButtonNone || msg.Button > tea.MouseButton11 {
		return
	}
	// #nosec G115 -- The validated X11 button codes are 0 through 11 in both libraries.
	event := uv.Mouse{X: x, Y: y, Button: uv.MouseButton(msg.Button)}
	if msg.Alt {
		event.Mod |= uv.ModAlt
	}
	if msg.Ctrl {
		event.Mod |= uv.ModCtrl
	}
	if msg.Shift {
		event.Mod |= uv.ModShift
	}
	switch {
	case msg.Action == tea.MouseActionRelease:
		m.term.SendMouse(uv.MouseReleaseEvent(event))
	case msg.Action == tea.MouseActionMotion:
		m.term.SendMouse(uv.MouseMotionEvent(event))
	case msg.Button >= tea.MouseButtonWheelUp && msg.Button <= tea.MouseButtonWheelRight:
		m.term.SendMouse(uv.MouseWheelEvent(event))
	default:
		m.term.SendMouse(uv.MouseClickEvent(event))
	}
}

// Close the input pipe directly: Emulator.Close also mutates a flag read by
// its blocking reader, which is not synchronized in x/vt v0.1.0.
func closeTerminal(term *vt.Emulator) {
	if closer, ok := term.InputPipe().(io.Closer); ok {
		_ = closer.Close()
	}
}
