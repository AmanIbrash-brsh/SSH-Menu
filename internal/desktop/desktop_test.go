package desktop

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
)

// zsh exports "_" with the command path in its internal encoding; from a folder
// like ~/Загрузки that is invalid UTF-8 and kitty crashes on it.
func TestCleanEnv(t *testing.T) {
	t.Setenv("_", "/home/u/\xd0\x83\xb7\xd0\xb0/ssh-menu")
	t.Setenv("SSHM_BROKEN", "a\xb7b")
	t.Setenv("SSHM_GOOD", "Загрузки")
	env := CleanEnv("SSH_MENU=1")
	joined := "\x00" + strings.Join(env, "\x00") + "\x00"
	for _, kv := range env {
		if !utf8.ValidString(kv) {
			t.Errorf("invalid UTF-8 passed on: %q", kv)
		}
	}
	if strings.Contains(joined, "\x00_=") || strings.Contains(joined, "SSHM_BROKEN") {
		t.Error("broken variables must be dropped")
	}
	if !strings.Contains(joined, "\x00SSHM_GOOD=Загрузки\x00") || !strings.Contains(joined, "\x00SSH_MENU=1\x00") {
		t.Error("valid variables and extras must be kept")
	}
}

func TestTerminalCommand(t *testing.T) {
	s := config.Settings{Terminal: "kitty", TerminalArgs: "-o font_size=12"}
	got := strings.Join(TerminalCommand(s, "ssh menu", "ssh-menu", []string{"/x/ssh-menu"}), "|")
	want := "kitty|--class|ssh-menu|--title|ssh menu|-o|remember_window_size=no|-o|initial_window_width=150c|-o|initial_window_height=38c|-o|font_size=12|/x/ssh-menu"
	if got != want {
		t.Errorf("kitty:\n got %s\nwant %s", got, want)
	}
	s = config.Settings{Terminal: "gnome-terminal"}
	if got := strings.Join(TerminalCommand(s, "t", "c", []string{"cmd", "arg"}), "|"); got != "gnome-terminal|--title|t|--geometry=150x38|--|cmd|arg" {
		t.Errorf("gnome-terminal: %s", got)
	}
}
