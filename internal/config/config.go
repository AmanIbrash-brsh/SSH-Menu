// Package config holds user settings and the locations of settings, state and cache files.
package config

import (
	"bufio"
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// Settings come from ~/.config/ssh-menu/config (KEY=value lines, shell style);
// a variable set in the environment wins over the file.
type Settings struct {
	Terminal     string // SSHM_TERMINAL: terminal for the popup and broadcast windows
	TerminalArgs string // SSHM_TERMINAL_ARGS: extra terminal options
	Browser      string // SSHM_BROWSER: command for Alt+W (default: xdg-open)
	Files        string // SSHM_FILES: file manager for Alt+F (default: nautilus, then xdg-open)
	Editor       string // SSHM_EDITOR: editor for Alt+E (default: $EDITOR, micro, nano, vim, vi)
}

func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func xdg(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	return filepath.Join(Home(), fallback)
}

// Dir is the settings directory (~/.config/ssh-menu).
func Dir() string { return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "ssh-menu") }

// File is the settings file.
func File() string { return filepath.Join(Dir(), "config") }

// StateDir is where history, caches and backups live (~/.local/state/ssh-menu); it is created on demand.
func StateDir() string {
	d := filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "ssh-menu")
	os.MkdirAll(d, 0o755)
	return d
}

// State returns a path inside StateDir.
func State(name string) string { return filepath.Join(StateDir(), name) }

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	// unquoted value: a trailing " # comment" is not part of it
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// parse reads KEY=value lines, ignoring comments, blank lines and an optional "export ".
func parse(path string) map[string]string {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		l = strings.TrimPrefix(l, "export ")
		if l == "" || l[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(l, "="); ok {
			m[strings.TrimSpace(k)] = unquote(v)
		}
	}
	return m
}

//go:embed config.example
var example []byte

// EnsureFile writes a commented example settings file on first run.
func EnsureFile() {
	if _, err := os.Stat(File()); os.IsNotExist(err) {
		os.MkdirAll(Dir(), 0o755)
		os.WriteFile(File(), example, 0o644)
	}
}

// Load returns the effective settings.
func Load() Settings {
	m := parse(File())
	get := func(k string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return m[k]
	}
	return Settings{
		Terminal:     get("SSHM_TERMINAL"),
		TerminalArgs: get("SSHM_TERMINAL_ARGS"),
		Browser:      get("SSHM_BROWSER"),
		Files:        get("SSHM_FILES"),
		Editor:       get("SSHM_EDITOR"),
	}
}

// WriteAtomic replaces a file in one step, so a reader never sees a half-written cache.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(mode)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
