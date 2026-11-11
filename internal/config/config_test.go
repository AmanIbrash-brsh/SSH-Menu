package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, k := range []string{"SSHM_TERMINAL", "SSHM_TERMINAL_ARGS", "SSHM_BROWSER", "SSHM_FILES", "SSHM_EDITOR"} {
		os.Unsetenv(k)
	}
	os.MkdirAll(filepath.Join(dir, "ssh-menu"), 0o755)
	os.WriteFile(File(), []byte(`# comment
SSHM_TERMINAL=kitty
SSHM_TERMINAL_ARGS="-o font_size=12"
export SSHM_BROWSER='flatpak run com.google.Chrome'
#SSHM_FILES=dolphin
SSHM_EDITOR=micro   # trailing comment
`), 0o644)
	s := Load()
	want := Settings{Terminal: "kitty", TerminalArgs: "-o font_size=12", Browser: "flatpak run com.google.Chrome", Editor: "micro"}
	if s != want {
		t.Errorf("got %+v\nwant %+v", s, want)
	}
	t.Setenv("SSHM_TERMINAL", "foot") // the environment wins over the file
	if Load().Terminal != "foot" {
		t.Error("environment must override the file")
	}
}

func TestWriteAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := WriteAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	WriteAtomic(p, []byte("two"), 0o600)
	b, _ := os.ReadFile(p)
	st, _ := os.Stat(p)
	if string(b) != "two" || st.Mode().Perm() != 0o600 {
		t.Errorf("%q %v", b, st.Mode())
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".tmp-*")); len(m) != 0 {
		t.Error("temp file left behind")
	}
}
