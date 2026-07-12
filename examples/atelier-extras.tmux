# atelier-extras.tmux — example user override snippet.
#
# COPY this file to ~/.config/atelier/tmux.conf.local to load it
# on next `atelier` start. Atelier's bundled config sources that
# path LAST, so anything you set here overrides every default the
# distribution ships with.
#
# This example demonstrates a powerline-style statusline. The
# atelier base mode ships a deliberately glyph-free default so
# fresh installs render correctly on stock fonts; this snippet
# is the "I want decoration" opt-in path.
#
# ┌────────────────────────────────────────────────────────────┐
# │ REQUIRES a Nerd Font (or a powerline-patched font).        │
# │ Without one, the , ,    glyphs below render as boxes.   │
# │ Install JetBrainsMono Nerd Font (or your favorite Nerd     │
# │ Font) and set it as your terminal font before using this.  │
# └────────────────────────────────────────────────────────────┘
#
# Atelier's freshness + attention + forge (PR badge) segments are
# injected by stamp-statusline AFTER this file is sourced, so anything you do
# to window-status-current-format here is picked up automatically —
# you don't need to manually splice the `#(atelier status ...)`
# bits in. They land after the window name marker (#W).

# --- powerline palette + accents ---
# Pick stock tmux color numbers so this works regardless of the
# user's terminal palette. Swap for your preferred scheme.
set -g status-style "bg=colour234,fg=colour250"

# Session name on the left, with a powerline cap on the right edge.
set -g status-left-length 40
set -g status-left "#[fg=colour234,bg=colour103,bold]  #S #[fg=colour103,bg=colour234,nobold]"

# Clock + date on the right, with a powerline cap on the left edge.
set -g status-right-length 60
set -g status-right "#[fg=colour240,bg=colour234]#[fg=colour250,bg=colour240] %H:%M #[fg=colour103,bg=colour240]#[fg=colour234,bg=colour103,bold] %d %b "

# Window list: powerline separators between windows. The current
# window gets the accent palette; inactive windows fade into the
# bar background.
set -g window-status-separator ""
set -g window-status-format "#[fg=colour234,bg=colour234]#[fg=colour244,bg=colour234] #I #W #[fg=colour234,bg=colour234]"
set -g window-status-current-format "#[fg=colour234,bg=colour103]#[fg=colour234,bg=colour103,bold] #I #W #[fg=colour103,bg=colour234,nobold]"

# --- pane border accent overrides ---
# Pop the active pane border for visual focus.
set -g pane-active-border-style "fg=colour103,bg=default"
set -g pane-border-style "fg=colour237,bg=default"

# --- statusline position ---
# Move the bar to the top — feels more IDE-like with powerline
# decoration. Comment out if you prefer the default (bottom).
set -g status-position top
