// Package desktop talks to the graphical session. Everything here is optional:
// when a tool is missing the feature degrades instead of failing.
package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
)

// Have reports whether a program is on PATH.
func Have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Detach starts a program that outlives the menu window.
func Detach(argv ...string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Notify shows a desktop notification, if notify-send exists.
func Notify(title, body string) {
	if Have("notify-send") {
		exec.Command("notify-send", "-a", "ssh-menu", title, body).Run()
	}
}

func quiet(argv ...string) { exec.Command(argv[0], argv[1:]...).Run() }

// Unfloat turns the floating popup into a normal tiled window once it becomes a session.
func Unfloat() {
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" && Have("hyprctl"):
		quiet("hyprctl", "dispatch", `hl.dsp.window.float({ action = "disable" })`) // Lua config
		quiet("hyprctl", "dispatch", "settiled", "active")                          // classic config
	case os.Getenv("SWAYSOCK") != "" && Have("swaymsg"):
		quiet("swaymsg", "floating", "disable")
	}
}

// Copy puts text on the clipboard; false when no clipboard tool exists.
func Copy(text string) bool {
	var cmd *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "" && Have("wl-copy"):
		cmd = exec.Command("wl-copy")
	case Have("xclip"):
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case Have("xsel"):
		cmd = exec.Command("xsel", "-b")
	default:
		return false
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// OpenURL opens a web page in the configured browser, or the default one.
func OpenURL(s config.Settings, url string) error {
	if s.Browser != "" {
		return Detach(append(strings.Fields(s.Browser), url)...)
	}
	return Detach("xdg-open", url)
}

// OpenFiles opens an sftp:// location in a file manager.
func OpenFiles(s config.Settings, url string) error {
	switch {
	case s.Files != "":
		return Detach(append(strings.Fields(s.Files), url)...)
	case Have("nautilus"):
		return Detach("nautilus", "--new-window", url)
	}
	return Detach("xdg-open", url)
}

// PickFiles shows a graphical file chooser. ok is false when there is none
// (the caller then falls back to the built-in browser).
func PickFiles(title string) (files []string, ok bool) {
	var cmd *exec.Cmd
	switch {
	case Have("zenity"):
		cmd = exec.Command("zenity", "--file-selection", "--multiple", "--separator=\n", "--title="+title, "--filename="+config.Home()+"/")
	case Have("kdialog"):
		cmd = exec.Command("kdialog", "--title", title, "--getopenfilename", config.Home(), "--multiple", "--separate-output")
	default:
		return nil, false
	}
	out, _ := cmd.Output()
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, true
}

// Terminals the popup knows how to start, in order of preference.
var Terminals = []string{"kitty", "foot", "alacritty", "wezterm", "ghostty", "konsole", "ptyxis", "gnome-terminal", "xfce4-terminal", "xterm"}

// Terminal returns the terminal to use: the configured one, else the first found.
func Terminal(s config.Settings) string {
	if s.Terminal != "" {
		return s.Terminal
	}
	for _, t := range Terminals {
		if Have(t) {
			return t
		}
	}
	return ""
}

// TerminalCommand builds the command that runs argv in a new terminal window.
// The window class lets a window manager float the menu (rule on "ssh-menu").
func TerminalCommand(s config.Settings, title, class string, argv []string) []string {
	t := Terminal(s)
	if t == "" {
		return nil
	}
	extra := strings.Fields(s.TerminalArgs)
	cat := func(parts ...[]string) []string {
		out := []string{t}
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	switch filepath.Base(t) {
	case "kitty":
		return cat([]string{"--class", class, "--title", title, "-o", "remember_window_size=no",
			"-o", "initial_window_width=150c", "-o", "initial_window_height=38c"}, extra, argv)
	case "foot":
		return cat([]string{"--app-id", class, "--title", title, "--window-size-chars=150x38"}, extra, argv)
	case "alacritty":
		return cat([]string{"--class", class, "--title", title, "-o", "window.dimensions.columns=150",
			"-o", "window.dimensions.lines=38"}, extra, []string{"-e"}, argv)
	case "wezterm":
		return cat([]string{"start", "--class", class}, extra, []string{"--"}, argv)
	case "ghostty":
		return cat([]string{"--title=" + title}, extra, []string{"-e"}, argv)
	case "ptyxis":
		return cat([]string{"--new-window"}, extra, []string{"--"}, argv)
	case "gnome-terminal":
		return cat([]string{"--title", title, "--geometry=150x38"}, extra, []string{"--"}, argv)
	case "xfce4-terminal":
		return cat([]string{"--title", title, "--geometry=150x38"}, extra, []string{"-x"}, argv)
	}
	return cat(extra, []string{"-e"}, argv)
}
