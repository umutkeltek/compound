# adapters/_lib/zshrc-snippet.zsh
#
# Drop ONE line near the bottom of your ~/.zshrc to wire up the lane system:
#
#     [ -r "$HOME/Projects/systems/compound/adapters/_lib/zshrc-snippet.zsh" ] \
#       && . "$HOME/Projects/systems/compound/adapters/_lib/zshrc-snippet.zsh"
#
# Or, if you already have it, just source adapters/_lib/zshrc-overlay.zsh for
# only the visual overlay (without the launcher functions).
#
# What this does:
#   1. Sources lane-launcher.sh — generates claude-zai, claude-minimax, etc.
#   2. Sources zshrc-overlay.zsh — adds chpwd/precmd hook that paints the
#      lane's iTerm2 tab color + lane-augmented title and badge.
#
# Both files are re-source safe. Order matters: launcher first, overlay second.
# The user's existing `_iterm_visuals_update` is preserved untouched.

# zsh passes through LANE_* env vars automatically. We don't need to export them.
LANE_LAUNCHER_FILE="$HOME/Projects/systems/compound/adapters/_lib/lane-launcher.sh"
[ -r "$LANE_LAUNCHER_FILE" ] && . "$LANE_LAUNCHER_FILE"

LANE_IT2_FILE="$HOME/Projects/systems/compound/adapters/_lib/lane-it2.sh"
[ -r "$HOME/Projects/systems/compound/adapters/_lib/zshrc-overlay.zsh" ] \
  && . "$HOME/Projects/systems/compound/adapters/_lib/zshrc-overlay.zsh"

