package agent

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/vyrwu/atelier/internal/core"
)

// Usage is an account observation, shared across spaces using the same agent
// config. It contains only quota metadata, never credentials or conversations.
type Usage struct {
	Observed time.Time     `json:"observed"`
	Source   string        `json:"source,omitempty"`
	Buckets  []UsageBucket `json:"buckets"`
}

type UsageBucket struct {
	ID        string       `json:"limitId"`
	Name      string       `json:"limitName"`
	Primary   *UsageWindow `json:"primary"`
	Secondary *UsageWindow `json:"secondary"`
}

type UsageWindow struct {
	Used    *float64 `json:"usedPercent"`
	Minutes int      `json:"windowDurationMins"`
	Reset   int64    `json:"resetsAt"`
	Native  bool     `json:"native,omitempty"`
}

// usagePath separates dev/prod caches and agent config directories, so another
// CODEX_HOME (or CLAUDE_CONFIG_DIR) cannot inherit this account's percentages.
func usagePath(k Kind) string {
	dir := codexHome()
	if k == Claude {
		dir = settingsPath()
	}
	hash := sha256.Sum256([]byte(dir))
	return filepath.Join(core.CacheDir(), "usage", fmt.Sprintf("%s-%x.json", k, hash[:6]))
}

func LoadUsage(k Kind) Usage {
	var u Usage
	data, _ := os.ReadFile(usagePath(k))
	_ = json.Unmarshal(data, &u)
	return u
}

func saveUsage(k Kind, u Usage) error {
	path := usagePath(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return writeConfig(path, data, 0o600)
}

// CaptureClaudeUsage reads the native status-line payload. Missing fields do
// not erase another session's last observation: startup/compaction can omit
// them, and expired windows are suppressed at render time instead.
func CaptureClaudeUsage(data []byte, now time.Time) error {
	var input struct {
		Limits map[string]*struct {
			Used  *float64 `json:"used_percentage"`
			Reset int64    `json:"resets_at"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	window := func(key string, minutes int) *UsageWindow {
		v := input.Limits[key]
		if v == nil || v.Used == nil {
			return nil
		}
		return &UsageWindow{Used: v.Used, Minutes: minutes, Reset: v.Reset}
	}
	b := UsageBucket{ID: "claude", Primary: window("five_hour", 300), Secondary: window("seven_day", 10080)}
	if b.Primary == nil && b.Secondary == nil {
		return nil
	}
	unlock, err := lockConfig(usagePath(Claude))
	if err != nil {
		return err
	}
	defer unlock()
	cached := LoadUsage(Claude)
	// /usage is an account-wide reading. A session may be replaying headers
	// from its last response. Preserve each applicable native window, but let
	// a newer reset revive an expired window when native refresh is unavailable.
	var prior UsageBucket
	if len(cached.Buckets) > 0 {
		prior = cached.Buckets[0]
		if cached.Source == "native" {
			// Cache compatibility: older builds marked only the snapshot.
			for _, w := range []*UsageWindow{prior.Primary, prior.Secondary} {
				if w != nil {
					w.Native = true
				}
			}
		}
	}
	merge := func(old, incoming *UsageWindow) (*UsageWindow, bool) {
		if incoming == nil || incoming.Reset <= now.Unix() || *incoming.Used < 0 || *incoming.Used > 100 {
			return old, false
		}
		if old != nil && ((old.Native && old.Reset > now.Unix()) || incoming.Reset < old.Reset || (old.Native && incoming.Reset == old.Reset)) {
			return old, false
		}
		if old != nil && incoming.Reset == old.Reset && old.Used != nil && *incoming.Used < *old.Used {
			return old, false // another session can replay an older same-window header
		}
		return incoming, true
	}
	var primaryChanged, secondaryChanged bool
	b.Primary, primaryChanged = merge(prior.Primary, b.Primary)
	b.Secondary, secondaryChanged = merge(prior.Secondary, b.Secondary)
	if !primaryChanged && !secondaryChanged {
		return nil
	}
	return saveUsage(Claude, Usage{Observed: now, Source: "statusline", Buckets: []UsageBucket{b}})
}

// decodeCodexUsage accepts both the current multi-bucket response and the older
// single-bucket response. Bucket identities come from Codex, never from a model
// list: several models can share one allowance.
func decodeCodexUsage(data []byte, now time.Time) (Usage, error) {
	var result struct {
		Single  *UsageBucket           `json:"rateLimits"`
		Buckets map[string]UsageBucket `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Usage{}, err
	}
	u := Usage{Observed: now}
	keys := make([]string, 0, len(result.Buckets))
	for key := range result.Buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b := result.Buckets[key]
		if b.ID == "" {
			b.ID = key
		}
		u.Buckets = append(u.Buckets, b)
	}
	if len(u.Buckets) == 0 && result.Single != nil {
		u.Buckets = append(u.Buckets, *result.Single)
	}
	return u, nil
}

const usageDim = "#[fg=colour245,nobold]"

// UsageStale is how old agent k's reading may get before the status job
// fetches it itself; an agent also refreshes its own reading when one of its
// turns ends. The fetch sets the age: Codex's app-server answers in a tenth of
// a second of CPU, so its reading stays a minute fresh. Claude's /usage boots
// the whole CLI, about 4s of CPU and 0.5GB, so its reading waits for a turn,
// its own status line while a session runs, or 15 minutes.
func UsageStale(k Kind) time.Duration {
	if k == Codex {
		return time.Minute
	}
	return 15 * time.Minute
}

// UsageStatus shows percentages spent, coloured by usage level.
// Session-reported readings and readings older than UsageStale are '~'.
// Expired data never implies an unused allowance.
func UsageStatus(claude, codex Usage, now time.Time) string {
	return renderUsage(Claude, claude, now) + usageDim + " · " + renderUsage(Codex, codex, now) + usageDim + " spent"
}

func renderUsage(k Kind, u Usage, now time.Time) string {
	glyph, color := "\uec82", 209
	if k == Codex {
		glyph, color = "\uec81", 39
	}
	text := fmt.Sprintf("#[fg=colour%d]%s", color, glyph) + usageDim
	if len(u.Buckets) == 0 {
		if k == Claude {
			return text + " 5h — 7d —"
		}
		return text + " —"
	}
	if (k == Claude && u.Source != "native") || now.Sub(u.Observed) > UsageStale(k)+time.Minute {
		text += "~"
	}
	for _, b := range u.Buckets {
		// Keep the two labelled Claude slots even when one field is omitted.
		// Hiding the slot made a partial response look like a complete balance.
		if k == Claude {
			if b.Primary == nil {
				b.Primary = &UsageWindow{Minutes: 300}
			}
			if b.Secondary == nil {
				b.Secondary = &UsageWindow{Minutes: 10080}
			}
		}
		if len(u.Buckets) > 1 {
			name := b.Name
			if name == "" {
				name = b.ID
			}
			// Provider labels are data in a tmux format: allow only short plain
			// identifiers, never control characters or style/format directives.
			var label []rune
			for _, r := range name {
				if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
					label = append(label, r)
				}
				if len(label) == 16 {
					break
				}
			}
			text += " " + string(label) + ":"
		}
		if b.Primary == nil && b.Secondary == nil {
			text += " —"
		}
		for _, w := range []*UsageWindow{b.Primary, b.Secondary} {
			if w == nil {
				continue
			}
			text += " " + usageDuration(w.Minutes) + " "
			if w.Used == nil || math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) || *w.Used < 0 || *w.Used > 100 || w.Reset <= now.Unix() {
				text += "—"
				continue
			}
			color := 71 // green: <75% spent
			if *w.Used >= 75 {
				color = 179 // amber
			}
			if *w.Used >= 90 {
				color = 167 // red
			}
			text += fmt.Sprintf("#[fg=colour%d]%d%%", color, int(math.Floor(*w.Used))) + usageDim
			if *w.Used >= 90 {
				mins := int(math.Ceil(time.Unix(w.Reset, 0).Sub(now).Minutes()))
				text += " ↻" + usageDuration(mins)
			}
		}
	}
	return text
}

func usageDuration(minutes int) string {
	if minutes >= 1440 {
		return fmt.Sprintf("%dd", minutes/1440)
	}
	if minutes >= 60 {
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}

const usageCaptureMark = "# atelier usage\n"
const usageInheritedMark = usageCaptureMark + "# inherited\n"

// InstallClaudeUsage captures native status-line data through project-local
// settings. Claude hot-reloads these even in sessions that predate this build,
// so switching to Codex does not depend on restarting all existing Claude
// sessions. Global settings and the existing status command's output survive.
func InstallClaudeUsage(root string) error {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil
	}
	path := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	local, status := map[string]any{}, map[string]any{}
	var previous []byte
	localStatus := false
	for _, p := range []string{settingsPath(), filepath.Join(root, ".claude", "settings.json"), path} {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		cfg := map[string]any{}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err // never overwrite invalid user settings
		}
		if p == path {
			local, previous = cfg, data
		}
		if v, ok := cfg["statusLine"]; ok {
			fields, _ := v.(map[string]any)
			if fields == nil {
				status = map[string]any{}
				localStatus = p == path
				continue
			}
			if p == path {
				command, hasCommand := fields["command"].(string)
				localStatus = hasCommand
				if strings.HasPrefix(command, usageCaptureMark) {
					original, err := unwrapUsageCommand(command)
					if err != nil {
						return err
					}
					if strings.HasPrefix(command, usageInheritedMark) || original == "" {
						// Inherit the current command, keeping local fields such
						// as padding. Command provenance is independent of them.
						delete(fields, "command")
						delete(fields, "type")
						localStatus = false
					} else {
						// Older wrappers did not record provenance. Preserve their
						// nonempty command as a local override rather than lose it.
						fields["command"] = original
					}
				}
			}
			for key, value := range fields {
				status[key] = value
			}
		}
	}
	original, _ := status["command"].(string)
	mark := usageInheritedMark
	if localStatus {
		mark = usageCaptureMark
	}
	capture := mark + "env "
	// Pin the shared cache even when a pre-existing Claude process inherited a
	// different XDG environment from the status job or a dev instance.
	cacheHome := filepath.Dir(core.CacheDir())
	capture += "XDG_CACHE_HOME=" + shellQuote(cacheHome) + " "
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		capture += "CLAUDE_CONFIG_DIR=" + shellQuote(dir) + " "
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(original))
	status["type"] = "command"
	status["command"] = capture + shellQuote(selfPath()) + " usage claude " + shellQuote(encoded)
	local["statusLine"] = status
	out, err := json.MarshalIndent(local, "", "  ")
	if err != nil || string(previous) == string(out) {
		return err
	}
	return writeConfig(path, out, 0o600)
}

func unwrapUsageCommand(command string) (string, error) {
	// Only the final (base64) argument is our saved original command.
	fields := strings.Fields(command)
	decoded, err := base64.StdEncoding.DecodeString(strings.Trim(fields[len(fields)-1], "'"))
	return string(decoded), err
}
