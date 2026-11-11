package app

import (
	"bytes"
	"os"
	"strings"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/fz"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

func helpFlag() string { return config.State("help-on") }

// menu shows the host list. Every key is bound to this same program run with an
// internal sub-command; the list itself comes from the caches, so it appears at
// once, and "start" then refreshes it in the background.
func menu() int {
	os.Remove(helpFlag())
	hosts.StepFilter("reset")

	var buf bytes.Buffer
	ui.List(&buf)
	items := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if buf.Len() == 0 {
		items = nil
	}

	S := fz.Quote(self)
	relist := "+reload-sync(" + S + " _list)+transform-list-label(" + S + " _label)"
	lastnew := fz.Quote(config.State("lastnew"))
	footer := ui.Keys("Enter", "ssh", "Alt+T", "tmux", "Alt+U/D", "files", "Alt+← →", "group", "Alt+H", "all keys")

	args := []string{
		"--ansi", "--multi", "--layout=reverse", "--height=100%", "--tiebreak=index",
		"--style=full:rounded", "--padding=1,2", "--highlight-line",
		"--prompt=  ", "--pointer=▌", "--gutter= ", "--marker=✓", "--info=inline-right", "--no-scrollbar",
		"--input-label= ssh ", "--list-label=" + hosts.Label(), "--preview-label= details ",
		"--ghost=type to filter, or user@host to connect to anything",
		"--footer=" + footer, "--footer-border=none",
		"--preview=" + S + " _preview {2}", "--preview-window=right:34%:wrap",
		"--bind=start:reload-sync(" + S + " _live)",
		"--bind=ctrl-r:reload-sync(" + S + " _refresh)+refresh-preview",
		"--bind=alt-h,f1:transform-preview-label(" + S + " _help)+refresh-preview",
		"--bind=alt-left:execute-silent(" + S + " _filter prev)" + relist,
		"--bind=alt-right:execute-silent(" + S + " _filter next)" + relist,
		"--bind=alt-p:execute-silent(" + S + " _filter prod)" + relist,
		"--bind=enter:become(" + S + " _connect {2} {q})",
		"--bind=alt-t:become(" + S + " _tmux {2})",
		"--bind=alt-f:execute-silent(" + S + " _sftp {2})+abort",
		"--bind=alt-y:execute-silent(" + S + " _copy {2})+abort",
		"--bind=alt-u:execute(" + S + " _upload {+2})",
		"--bind=alt-d:execute(" + S + " _download {+2})",
		"--bind=alt-k:execute(" + S + " _keys {+2})+reload-sync(" + S + " _list)",
		"--bind=alt-n:execute(" + S + " _new)+reload-sync(" + S + " _live)+transform-query(cat " + lastnew + " 2>/dev/null; rm -f " + lastnew + ")",
		"--bind=alt-e:execute(" + S + " _edit {2})+reload-sync(" + S + " _live)",
		"--bind=alt-g:execute(" + S + " _group {+2})" + relist,
		"--bind=alt-r:execute-silent(" + S + " _prod {+2})" + relist,
		"--bind=alt-b:execute(" + S + " _broadcast {+2})+abort",
		"--bind=alt-x:execute(" + S + " _run {+2})",
		"--bind=alt-w:execute(" + S + " _web {2})+reload-sync(" + S + " _list)+abort",
		"--bind=alt-a:select-all,alt-z:deselect-all",
	}
	args = append(args, fz.Theme...)
	_, code := fz.Run(items, args...)
	if code == fz.Interrupt || code == fz.NoMatch {
		return 0
	}
	return code
}
