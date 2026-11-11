# Open a command in a new terminal window: term_run <title> <window class> <command...>
# Terminal: $SSHM_TERMINAL, otherwise the first one found. Extra options: $SSHM_TERMINAL_ARGS.
# The window class lets a window manager float the menu (e.g. Hyprland/Sway rule on "ssh-menu").
term_pick() {
    local t
    if [ -n "$SSHM_TERMINAL" ]; then echo "$SSHM_TERMINAL"; return; fi
    for t in kitty foot alacritty wezterm ghostty konsole ptyxis gnome-terminal xfce4-terminal xterm; do
        command -v "$t" >/dev/null 2>&1 && { echo "$t"; return; }
    done
    return 1
}
term_run() {
    local title=$1 cls=$2; shift 2
    local t; t=$(term_pick) || { echo "ssh-menu: no terminal emulator found, set SSHM_TERMINAL" >&2; return 1; }
    local -a extra; read -r -a extra <<< "${SSHM_TERMINAL_ARGS:-}"
    case "${t##*/}" in
        kitty)     exec "$t" --class "$cls" --title "$title" -o remember_window_size=no \
                        -o initial_window_width=150c -o initial_window_height=38c "${extra[@]}" "$@" ;;
        foot)      exec "$t" --app-id "$cls" --title "$title" --window-size-chars=150x38 "${extra[@]}" "$@" ;;
        alacritty) exec "$t" --class "$cls" --title "$title" -o window.dimensions.columns=150 \
                        -o window.dimensions.lines=38 "${extra[@]}" -e "$@" ;;
        wezterm)   exec "$t" start --class "$cls" "${extra[@]}" -- "$@" ;;
        ghostty)   exec "$t" --title="$title" "${extra[@]}" -e "$@" ;;
        konsole)   exec "$t" "${extra[@]}" -e "$@" ;;
        ptyxis)    exec "$t" --new-window "${extra[@]}" -- "$@" ;;
        gnome-terminal) exec "$t" --title "$title" --geometry=150x38 "${extra[@]}" -- "$@" ;;
        xfce4-terminal) exec "$t" --title "$title" --geometry=150x38 "${extra[@]}" -x "$@" ;;
        *)         exec "$t" "${extra[@]}" -e "$@" ;;
    esac
}
