package cli

import (
	"strings"
	"testing"
)

// TestAtelierStatuslineRe_StripsPriorInjections locks in the
// idempotency contract: re-running `atelier init` must not duplicate
// atelier's status-line segments. We strip prior injections by
// matching `#(atelier status (freshness|attention)...)` and removing
// the leading whitespace too.
//
// Failure here would mean every dev iteration re-sources init and
// the format grows unbounded — which is exactly the bug we just
// fixed (status bar had 18+ duplicates of attention --count).
func TestAtelierStatuslineRe_StripsPriorInjections(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single freshness injection stripped",
			in:   `#W #(atelier status freshness 'a' 'b' 'c' 'd' 'e')`,
			want: `#W`,
		},
		{
			name: "single attention injection stripped (legacy --count form)",
			in:   `#W #(atelier status attention --count)`,
			want: `#W`,
		},
		{
			name: "single attention injection stripped (canonical count form)",
			in:   `#W #(atelier status attention count)`,
			want: `#W`,
		},
		{
			name: "single forge injection stripped",
			in:   `#W #(atelier status forge '#{@forge_state}')`,
			want: `#W`,
		},
		{
			name: "all three injections stripped",
			in:   `#W #(atelier status freshness 'a' 'b' 'c' 'd' 'e')#(atelier status attention count)#(atelier status forge '#{@forge_state}')`,
			want: `#W`,
		},
		{
			name: "many duplicates all stripped (the actual bug)",
			in: `#W` +
				`#(atelier status attention --count)` +
				`#(atelier status attention --count)` +
				`#(atelier status freshness 'a' 'b' 'c' 'd' 'e')` +
				`#(atelier status attention --count)` +
				`#(atelier status freshness 'a' 'b' 'c' 'd' 'e')`,
			want: `#W`,
		},
		{
			name: "user theme content preserved verbatim",
			in:   `#[fg=#44475a]#[bg=#6272a4] #W #[default]#(atelier status attention --count)`,
			want: `#[fg=#44475a]#[bg=#6272a4] #W #[default]`,
		},
		{
			name: "foreign #(...) calls NOT stripped",
			in:   `#W #(some_other_helper) #(atelier status freshness 'a' 'b' 'c' 'd' 'e')`,
			want: `#W #(some_other_helper)`,
		},
		{
			name: "empty input",
			in:   ``,
			want: ``,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := atelierStatuslineRe.ReplaceAllString(tc.in, "")
			if got != tc.want {
				t.Errorf("strip\n in:   %q\n got:  %q\n want: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSegmentOrder_CurrentFormat pins the visual order the user
// explicitly requested for window-status-current-format: freshness
// icon, THEN attention rollup, THEN forge PR badge. The bug where
// attention rendered first was caused by duplicate-append accumulation;
// the forge badge must land AFTER attention (spec: "after the
// attention icon").
func TestSegmentOrder_CurrentFormat(t *testing.T) {
	got := "" + freshnessSegment() + attentionSegment() + forgeSegment()
	fIdx := strings.Index(got, freshnessEmitter)
	aIdx := strings.Index(got, attentionEmitter)
	gIdx := strings.Index(got, forgeEmitter)
	if fIdx < 0 || aIdx < 0 || gIdx < 0 {
		t.Fatalf("segments missing: freshness=%d attention=%d forge=%d in %q", fIdx, aIdx, gIdx, got)
	}
	if fIdx >= aIdx || aIdx >= gIdx {
		t.Errorf("order must be freshness < attention < forge. got %q", got)
	}
}

// TestInjectAfterWindowName locks in the insertion-anchor behavior:
// atelier injects AFTER `#W` PLUS the powerline color-transition
// blocks that follow it. The transition (`#[fg=X]#[bg=Y]`) draws the
// powerline arrow exiting the window-name segment; injecting BEFORE
// it would land the icon inside the colored box, ahead of the arrow
// head — breaking the layout.
func TestInjectAfterWindowName(t *testing.T) {
	cases := []struct {
		name      string
		format    string
		injection string
		want      string
	}{
		{
			name:      "powerline: skips trailing color transition",
			format:    `#[fg=A]#[bg=B] #W #[fg=B]#[bg=C]#(user-helper)`,
			injection: `<INJ>`,
			want:      `#[fg=A]#[bg=B] #W #[fg=B]#[bg=C]<INJ>#(user-helper)`,
		},
		{
			name:      "no trailing colors: injects right after #W and any spaces",
			format:    `#W #(some_user_helper)`,
			injection: `<X>`,
			want:      `#W <X>#(some_user_helper)`,
		},
		{
			name:      "no trailing content: injects after #W",
			format:    `#W`,
			injection: `<X>`,
			want:      `#W<X>`,
		},
		{
			name:      "multiple color transitions all skipped",
			format:    `#W #[fg=a]#[bg=b]#[default]#(thing)`,
			injection: `<X>`,
			want:      `#W #[fg=a]#[bg=b]#[default]<X>#(thing)`,
		},
		{
			name:      "empty injection is no-op",
			format:    `#W #[default]`,
			injection: ``,
			want:      `#W #[default]`,
		},
		{
			// FR-2.4: no #W → no inject. Prior behavior was to append,
			// which produced a free-floating freshness icon per inactive
			// window (the "phantom second checkmark" bug). Skipping the
			// inject leaves the user's format intact; doctor flags it.
			name:      "no inject when #W absent",
			format:    `just #I:status`,
			injection: `<X>`,
			want:      `just #I:status`,
		},
		{
			name:      "no inject when format is empty",
			format:    ``,
			injection: `<X>`,
			want:      ``,
		},
		{
			name: "the user's actual format: freshness lands after the powerline arrow",
			format: `#[fg=#44475a]#[bg=#6272a4]#[fg=#f8f8f2]#[bg=#6272a4] #W #[fg=#6272a4]` +
				`#[bg=#44475a]#(tmux_count_attention)`,
			injection: `<X>`,
			want: `#[fg=#44475a]#[bg=#6272a4]#[fg=#f8f8f2]#[bg=#6272a4] #W #[fg=#6272a4]` +
				`#[bg=#44475a]<X>#(tmux_count_attention)`,
		},
		{
			// The user's REAL format includes a Powerline arrow
			// glyph () BETWEEN the exit color block and the
			// next segment's content. Without consuming the glyph,
			// atelier injects BEFORE the arrow — inside the window-
			// name's colored box. The injection must land AFTER the
			// arrow to render in the next segment.
			name: "powerline glyph after color exit is skipped too",
			format: "#[fg=#44475a]#[bg=#6272a4]#[fg=#f8f8f2]#[bg=#6272a4] " +
				"#W #[fg=#6272a4]#[bg=#44475a]#(tmux_count_attention)",
			injection: `<X>`,
			want: "#[fg=#44475a]#[bg=#6272a4]#[fg=#f8f8f2]#[bg=#6272a4] " +
				"#W #[fg=#6272a4]#[bg=#44475a]<X>#(tmux_count_attention)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := injectAfterWindowName(tc.format, tc.injection)
			if got != tc.want {
				t.Errorf("injectAfterWindowName\n in:   %q\n inj:  %q\n got:  %q\n want: %q",
					tc.format, tc.injection, got, tc.want)
			}
		})
	}
}
