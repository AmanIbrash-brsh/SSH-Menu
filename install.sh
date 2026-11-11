#!/bin/bash
# Install SSH Menu for the current user (no root needed).
#   ./install.sh                 install / update into ~/.local
#   PREFIX=/opt/x ./install.sh   install somewhere else
#   SSHM_BUNDLE_FZF=1 ./install.sh   always use a bundled fzf, even if the system one is new enough
set -euo pipefail

FZF_VERSION=0.74.5   # bundled when the system fzf is missing or older than FZF_MIN
FZF_MIN=0.63

PREFIX=${PREFIX:-$HOME/.local}
DEST=$PREFIX/share/ssh-menu
BIN=$PREFIX/bin
SRC=$(cd "$(dirname "$0")" && pwd)
CONF=${XDG_CONFIG_HOME:-$HOME/.config}/ssh-menu
APPS=${XDG_DATA_HOME:-$HOME/.local/share}/applications

ok()   { printf '\e[32m✓\e[0m %s\n' "$*"; }
warn() { printf '\e[33m!\e[0m %s\n' "$*"; }
die()  { printf '\e[31m✗\e[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
fzf_ok() {  # fzf_ok <binary>: is it at least FZF_MIN?
    local v; v=$("$1" --version 2>/dev/null | awk '{print $1}')
    [ -n "$v" ] && [ "$(printf '%s\n' "$FZF_MIN" "$v" | sort -V | head -1)" = "$FZF_MIN" ]
}
fetch() {
    if have curl; then curl -fsSL -o "$2" "$1"
    elif have wget; then wget -qO "$2" "$1"
    else die "need curl or wget to download fzf"; fi
}

echo "Installing SSH Menu to $DEST"

# ---- required ----
have ssh || die "ssh is required (package openssh-client / openssh-clients)"
have python3 || die "python3 is required"
python3 -c 'import sys; sys.exit(sys.version_info < (3, 8))' || die "python3 >= 3.8 is required"

# ---- files ----
mkdir -p "$PREFIX/share" "$BIN"
rm -rf "$DEST.new"
mkdir -p "$DEST.new"
cp -r "$SRC/bin" "$SRC/lib" "$SRC/share" "$DEST.new/"
[ -d "$DEST/libexec" ] && mv "$DEST/libexec" "$DEST.new/"   # keep a previously bundled fzf
rm -rf "$DEST"
mv "$DEST.new" "$DEST"
chmod +x "$DEST"/bin/*
ln -sfn "$DEST/bin/ssh-menu" "$BIN/ssh-menu"
ln -sfn "$DEST/bin/ssh-menu-popup" "$BIN/ssh-menu-popup"
ok "commands: $BIN/ssh-menu, $BIN/ssh-menu-popup"

# ---- fzf ----
if [ -z "${SSHM_BUNDLE_FZF:-}" ] && have fzf && fzf_ok fzf; then
    rm -rf "$DEST/libexec"
    ok "fzf $(fzf --version | awk '{print $1}') (system)"
elif [ -x "$DEST/libexec/fzf" ] && fzf_ok "$DEST/libexec/fzf"; then
    ok "fzf $("$DEST/libexec/fzf" --version | awk '{print $1}') (bundled)"
else
    case $(uname -m) in
        x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; armv7l) arch=armv7 ;;
        *) die "no fzf build for $(uname -m): install fzf >= $FZF_MIN yourself" ;;
    esac
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    base=https://github.com/junegunn/fzf/releases/download/v$FZF_VERSION
    tgz=fzf-$FZF_VERSION-linux_$arch.tar.gz
    echo "  system fzf is missing or older than $FZF_MIN, downloading fzf $FZF_VERSION …"
    fetch "$base/$tgz" "$tmp/$tgz"
    fetch "$base/fzf_${FZF_VERSION}_checksums.txt" "$tmp/sums"
    (cd "$tmp" && grep " $tgz\$" sums | sha256sum -c --status) || die "fzf download: checksum mismatch"
    mkdir -p "$DEST/libexec"
    tar -xzf "$tmp/$tgz" -C "$DEST/libexec" fzf
    chmod +x "$DEST/libexec/fzf"
    ok "fzf $FZF_VERSION (bundled, sha256 verified)"
fi

# ---- config ----
if [ ! -e "$CONF/config" ]; then
    mkdir -p "$CONF"
    cp "$SRC/config.example" "$CONF/config"
    ok "settings: $CONF/config"
else
    ok "settings kept: $CONF/config"
fi

# ---- app menu entry ----
mkdir -p "$APPS"
sed "s|@BIN@|$BIN|g" "$SRC/share/ssh-menu.desktop.in" > "$APPS/ssh-menu.desktop"
ok "app menu entry: SSH Menu"

# ---- optional tools ----
echo
missing=()
. "$DEST/lib/term.sh"
t=$(term_pick 2>/dev/null) && ok "terminal: ${t##*/}" || missing+=("a terminal emulator (kitty, foot, alacritty, …)")
have tmux || missing+=("tmux (Alt+B broadcast)")
{ have wl-copy || have xclip || have xsel; } || missing+=("wl-clipboard / xclip (Alt+Y copy)")
{ have zenity || have kdialog; } || missing+=("zenity / kdialog (Alt+U file picker; fzf is used otherwise)")
have notify-send || missing+=("notify-send / libnotify (notifications)")
for m in "${missing[@]}"; do warn "optional, not found: $m"; done

case ":$PATH:" in *":$BIN:"*) ;; *) warn "$BIN is not in PATH: add it, or call $BIN/ssh-menu directly" ;; esac

# ---- hotkey hint ----
de=${XDG_CURRENT_DESKTOP:-}
echo
echo "Hotkey: bind  $BIN/ssh-menu-popup  to a key (e.g. Super+S):"
case "$de" in
    *Hyprland*) echo "  hyprland.conf:  bind = SUPER, S, exec, $BIN/ssh-menu-popup"
                echo "                  windowrulev2 = float, class:^(ssh-menu)\$"
                echo "                  windowrulev2 = size 1400 780, class:^(ssh-menu)\$" ;;
    *sway*)     echo "  sway config:    bindsym \$mod+s exec $BIN/ssh-menu-popup"
                echo "                  for_window [app_id=\"ssh-menu\"] floating enable, resize set 1400 780" ;;
    *GNOME*)    echo "  Settings → Keyboard → View and Customize Shortcuts → Custom Shortcuts → +"
                echo "  command: $BIN/ssh-menu-popup   (GNOME uses Super+S for quick settings: pick another key or free it)" ;;
    *KDE*)      echo "  System Settings → Keyboard → Shortcuts → Add New → Command or Script → $BIN/ssh-menu-popup" ;;
    *)          echo "  see README.md, section «Горячая клавиша»" ;;
esac
echo
ok "done. Run: ssh-menu"
