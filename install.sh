#!/bin/sh
# Install SSH Menu for the current user (no root needed).
#
#   curl -fsSL https://raw.githubusercontent.com/AmanIbrash-brsh/SSH-Menu/main/install.sh | sh
#
#   ./install.sh                 download the latest release
#   ./install.sh --source        build from this checkout (needs Go)
#   ./install.sh --binary FILE   install a binary you already have
#   PREFIX=/opt/x ./install.sh   install somewhere other than ~/.local
set -eu

REPO=AmanIbrash-brsh/SSH-Menu
PREFIX=${PREFIX:-$HOME/.local}
BIN=$PREFIX/bin
APPS=${XDG_DATA_HOME:-$HOME/.local/share}/applications

ok()   { printf '\033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[33m!\033[0m %s\n' "$*"; }
die()  { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
fetch() {
    if have curl; then curl -fsSL -o "$2" "$1"
    elif have wget; then wget -qO "$2" "$1"
    else die "need curl or wget"; fi
}

mode=release; file=
case "${1:-}" in
    --source) mode=source ;;
    --binary) mode=binary; file=${2:-}; [ -f "$file" ] || die "--binary needs a file" ;;
    "") ;;
    *) die "unknown option: $1" ;;
esac

have ssh || warn "ssh is not installed yet (package openssh-clients / openssh-client): the menu needs it"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

case $mode in
release)
    case $(uname -s) in Linux) ;; *) die "SSH Menu supports Linux only" ;; esac
    case $(uname -m) in
        x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;;
        *) die "no release build for $(uname -m): clone the repository and run ./install.sh --source" ;;
    esac
    tgz=ssh-menu_linux_$arch.tar.gz
    base=https://github.com/$REPO/releases/latest/download
    echo "Downloading the latest SSH Menu ($arch) …"
    fetch "$base/$tgz" "$tmp/$tgz" || die "download failed: $base/$tgz"
    fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: checksums.txt"
    (cd "$tmp" && grep " $tgz\$" checksums.txt | sha256sum -c - >/dev/null 2>&1) || die "checksum mismatch, nothing installed"
    tar -xzf "$tmp/$tgz" -C "$tmp" ssh-menu
    file=$tmp/ssh-menu
    ok "downloaded, sha256 verified"
    ;;
source)
    have go || die "--source needs Go (https://go.dev/dl)"
    src=$(cd "$(dirname "$0")" && pwd)
    ver=$(cd "$src" && git describe --tags --always --dirty 2>/dev/null || echo dev)
    echo "Building SSH Menu $ver …"
    (cd "$src" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$tmp/ssh-menu" ./cmd/ssh-menu) || die "build failed"
    file=$tmp/ssh-menu
    ok "built from source"
    ;;
esac

mkdir -p "$BIN"
rm -f "$BIN/ssh-menu"            # may be a link left by the old bash version: never write through it
cp "$file" "$BIN/ssh-menu"
chmod 755 "$BIN/ssh-menu"
ok "installed: $BIN/ssh-menu ($("$BIN/ssh-menu" version))"

# hotkeys set up for the old bash version called ssh-menu-popup: keep them working
rm -f "$BIN/ssh-menu-popup"
printf '#!/bin/sh\nexec "%s/ssh-menu" popup "$@"\n' "$BIN" > "$BIN/ssh-menu-popup"
chmod 755 "$BIN/ssh-menu-popup"

mkdir -p "$APPS"
cat > "$APPS/ssh-menu.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=SSH Menu
Comment=Pick a server from ~/.ssh/config and connect
Exec=$BIN/ssh-menu popup
Icon=utilities-terminal
Terminal=false
Categories=System;Network;RemoteAccess;
Keywords=ssh;server;sftp;tmux;
StartupWMClass=ssh-menu
DESKTOP
ok "app menu entry: SSH Menu"

if [ -d "$PREFIX/share/ssh-menu/lib" ]; then
    warn "the old bash version is still in $PREFIX/share/ssh-menu: it is no longer used, you can delete that folder"
fi
case ":$PATH:" in *":$BIN:"*) ;; *) warn "$BIN is not in PATH: add it, or call $BIN/ssh-menu directly" ;; esac

echo
"$BIN/ssh-menu" doctor || true
echo
echo "Hotkey: bind  $BIN/ssh-menu popup  to a key (e.g. Super+S)."
case "${XDG_CURRENT_DESKTOP:-}" in
    *Hyprland*) echo "  hyprland.conf:  bind = SUPER, S, exec, $BIN/ssh-menu popup"
                echo "                  windowrulev2 = float, class:^(ssh-menu)\$"
                echo "                  windowrulev2 = size 1400 780, class:^(ssh-menu)\$" ;;
    *sway*)     echo "  sway config:    bindsym \$mod+s exec $BIN/ssh-menu popup"
                echo "                  for_window [app_id=\"ssh-menu\"] floating enable, resize set 1400 780" ;;
    *GNOME*)    echo "  Settings → Keyboard → View and Customize Shortcuts → Custom Shortcuts → +"
                echo "  (GNOME uses Super+S for quick settings: pick another key or free it)" ;;
    *KDE*)      echo "  System Settings → Keyboard → Shortcuts → Add New → Command or Script" ;;
    *)          echo "  see README.md, section «Горячая клавиша»" ;;
esac
echo
ok "done. Run: ssh-menu"
