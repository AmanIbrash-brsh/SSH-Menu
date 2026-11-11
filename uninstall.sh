#!/bin/bash
# Remove SSH Menu. Your ~/.ssh/config, settings (~/.config/ssh-menu) and history
# (~/.local/state/ssh-menu) are kept; delete them by hand if you want.
set -eu
PREFIX=${PREFIX:-$HOME/.local}
DEST=$PREFIX/share/ssh-menu
for c in ssh-menu ssh-menu-popup; do
    l=$PREFIX/bin/$c
    # only remove our own links
    [ -L "$l" ] && [ "$(readlink "$l")" = "$DEST/bin/$c" ] && rm -f "$l"
done
rm -rf "$DEST"
rm -f "${XDG_DATA_HOME:-$HOME/.local/share}/applications/ssh-menu.desktop"
echo "SSH Menu removed. Kept: ${XDG_CONFIG_HOME:-$HOME/.config}/ssh-menu, ${XDG_STATE_HOME:-$HOME/.local/state}/ssh-menu"
