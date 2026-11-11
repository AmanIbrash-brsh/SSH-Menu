#!/bin/sh
# Remove SSH Menu. Your ~/.ssh/config, settings (~/.config/ssh-menu) and history
# (~/.local/state/ssh-menu) are kept; delete them by hand if you want.
set -eu
PREFIX=${PREFIX:-$HOME/.local}
rm -f "$PREFIX/bin/ssh-menu" "$PREFIX/bin/ssh-menu-popup"
rm -rf "$PREFIX/share/ssh-menu"   # left by the old bash version
rm -f "${XDG_DATA_HOME:-$HOME/.local/share}/applications/ssh-menu.desktop"
echo "SSH Menu removed. Kept: ${XDG_CONFIG_HOME:-$HOME/.config}/ssh-menu, ${XDG_STATE_HOME:-$HOME/.local/state}/ssh-menu"
