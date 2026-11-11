package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

const cHi = "\x1b[38;2;233;30;99m" // accent for prompts

func say(a ...any) { fmt.Println(a...) }

func clear() { fmt.Print("\x1b[H\x1b[2J") }

func okLine(s string) string   { return ui.Green + "✓" + ui.Reset + " " + s }
func failLine(s string) string { return ui.Red + "✗" + ui.Reset + " " + s }

// ask reads one line with basic editing. An empty answer means def.
// ok is false when the user cancelled with Ctrl+C / Ctrl+D.
func ask(prompt, def string) (answer string, ok bool) {
	p := cHi + prompt + ui.Reset + " "
	if def != "" {
		p += "[" + def + "] "
	}
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil { // not a terminal: plain read
		fmt.Print(p)
		var s string
		fmt.Scanln(&s)
		if s == "" {
			s = def
		}
		return s, true
	}
	t := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stdout}, p)
	line, err := t.ReadLine()
	term.Restore(fd, old)
	if err != nil {
		fmt.Println()
		return "", false
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = def
	}
	return line, true
}

// key waits for a single key press.
func key() rune {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		var s string
		fmt.Scanln(&s)
		for _, r := range s {
			return r
		}
		return '\n'
	}
	defer term.Restore(fd, old)
	b := make([]byte, 8)
	n, _ := os.Stdin.Read(b)
	r, _ := utf8.DecodeRune(b[:n])
	return r
}

// confirm asks a yes/no question; Enter picks the default, anything unclear too.
// Russian layout works as well: д = да, н = нет.
func confirm(prompt string, defYes bool) bool {
	hint := "[y/N]"
	if defYes {
		hint = "[Y/n]"
	}
	fmt.Printf("%s%s%s %s ", cHi, prompt, ui.Reset, hint)
	k := key()
	fmt.Println()
	switch k {
	case 'y', 'Y', 'д', 'Д':
		return true
	case 'n', 'N', 'н', 'Н', 3, 27: // no, Ctrl+C, Esc
		return false
	}
	return defYes
}

func pause() {
	fmt.Print(ui.Dim + "press any key" + ui.Reset)
	key()
	fmt.Println()
}
