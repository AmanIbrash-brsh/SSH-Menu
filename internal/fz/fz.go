// Package fz runs the embedded fzf finder.
package fz

import (
	"strings"

	fzf "github.com/junegunn/fzf/src"
)

// Exit codes of the finder.
const (
	OK        = fzf.ExitOk
	NoMatch   = fzf.ExitNoMatch
	Interrupt = fzf.ExitInterrupt
)

// Theme is the colour scheme shared by every picker.
var Theme = []string{
	"--color=bg+:#2a2024,fg+:#ffffff,hl:#e91e63,hl+:#ff6398,pointer:#e91e63,prompt:#e91e63,info:#8e7a7d,marker:#f8bbd9,spinner:#e91e63,ghost:#6b5a5e,gutter:-1",
	"--color=border:#3a2a2e,list-border:#3a2a2e,preview-border:#3a2a2e,input-border:#e91e63,header-border:#3a2a2e",
	"--color=label:#e91e63,input-label:#e91e63,list-label:#8e7a7d,preview-label:#8e7a7d,header-label:#8e7a7d,footer:-1,header:-1",
}

// Run shows items in fzf with the given options and returns the printed lines
// and the exit code. Commands in --bind run through /bin/sh, which starts
// faster than an interactive user shell.
func Run(items []string, args ...string) ([]string, int) {
	args = append([]string{"--with-shell", "sh -c"}, args...)
	opts, err := fzf.ParseOptions(false, args)
	if err != nil {
		return []string{err.Error()}, fzf.ExitError
	}
	in, out := make(chan string), make(chan string)
	opts.Input, opts.Output = in, out
	go func() {
		for _, it := range items {
			in <- it
		}
		close(in)
	}()
	var lines []string
	done := make(chan struct{})
	go func() {
		for l := range out {
			lines = append(lines, l)
		}
		close(done)
	}()
	code, _ := fzf.Run(opts)
	close(out)
	<-done
	return lines, code
}

// Quote makes a string safe as one word of a /bin/sh command line.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
