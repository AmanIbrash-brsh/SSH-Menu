package app

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/desktop"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/fz"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

// picker is a small fzf with the menu's look, used for sub-dialogs.
func picker(items []string, prompt string, extra ...string) ([]string, int) {
	args := []string{"--layout=reverse", "--height=100%", "--border=rounded", "--prompt=" + prompt, "--pointer=▌",
		"--no-scrollbar", "--info=inline-right", "--ansi"}
	args = append(args, fz.Theme...)
	return fz.Run(items, append(args, extra...)...)
}

// lister returns the absolute path of dir and its entries (folders end with "/").
type lister func(dir string) (cwd string, entries []string, err error)

func remoteLister(host string) lister {
	return func(dir string) (string, []string, error) {
		out, err := exec.Command("ssh", host, "cd -- "+fz.Quote(dir)+" 2>/dev/null || cd; pwd; ls -1Ap").Output()
		if err != nil {
			return "", nil, fmt.Errorf("cannot list files on %s", host)
		}
		l := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		return l[0], l[1:], nil
	}
}

func localLister(dir string) (string, []string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	es, err := os.ReadDir(abs)
	if err != nil { // unreadable folder: stay usable
		return abs, nil, nil
	}
	var dirs, files []string
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(abs, e.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, e.Name()+"/")
		} else {
			files = append(files, e.Name())
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	return abs, append(dirs, files...), nil
}

// browse walks folders and returns the chosen absolute paths.
// Enter opens a folder or takes a file; takeKey takes marked entries or a whole folder; ← goes up.
func browse(title string, ls lister, start, takeKey string) []string {
	dir := start
	label := "Alt+" + strings.ToUpper(takeKey[len(takeKey)-1:]) // alt-d -> Alt+D
	header := ui.Keys("Enter", "open folder / take file", "Tab", "mark", label, "take marked or folder", "←", "up")
	for {
		cwd, entries, err := ls(dir)
		if err != nil {
			say(ui.Red + err.Error() + ui.Reset)
			pause()
			return nil
		}
		cwd = strings.TrimRight(cwd, "/")
		items := append([]string{"../"}, entries...)
		out, code := picker(items, title+cwd+"/ ", "--multi", "--marker=✓", "--expect="+takeKey+",left", "--header="+header)
		if code != fz.OK || len(out) < 2 {
			return nil
		}
		keyName, picks := out[0], out[1:]
		if keyName == "left" {
			dir = cwd + "/.."
			continue
		}
		if keyName == takeKey || len(picks) > 1 {
			var res []string
			for _, p := range picks {
				if p != "../" {
					res = append(res, cwd+"/"+strings.TrimSuffix(p, "/"))
				}
			}
			return res
		}
		switch p := picks[0]; {
		case p == "../":
			dir = cwd + "/.."
		case strings.HasSuffix(p, "/"):
			dir = cwd + "/" + strings.TrimSuffix(p, "/")
		default:
			return []string{cwd + "/" + p}
		}
	}
}

// confirmProd asks before touching PROD hosts (default: no). True when it is fine to go on.
func confirmProd(aliases []string) bool {
	var prod []string
	for _, a := range aliases {
		if h := sshcfg.Find(a); h != nil && h.IsProd() {
			prod = append(prod, a)
		}
	}
	if len(prod) == 0 {
		return true
	}
	fmt.Printf("%s%d of %d hosts are PROD:%s %s\n", ui.Red, len(prod), len(aliases), ui.Reset, strings.Join(prod, " "))
	return confirm("continue?", false)
}

// needsPrompt tells whether some host cannot log in without asking (so it cannot run in the background).
func needsPrompt(aliases []string) bool {
	auth := hosts.LoadAuth()
	for _, a := range aliases {
		if s := auth[a].State; s != "key" {
			return true
		}
	}
	return false
}

func cmdUpload(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	files, gui := desktop.PickFiles("Upload to: " + strings.Join(aliases, " "))
	if !gui {
		files = browse("upload ❯ ", localLister, config.Home(), "alt-u")
	}
	if len(files) == 0 {
		return
	}
	clear()
	fmt.Printf("%supload%s  %s→ %s%s\n", cHi, ui.Reset, ui.Dim, strings.Join(aliases, " "), ui.Reset)
	for _, f := range files {
		say("  " + f)
	}
	say()
	dest, ok := ask("remote folder", "~")
	if !ok || !confirmProd(aliases) {
		return
	}
	say()
	size := localSize(files)
	// several hosts run in parallel and must not stop to ask for a password
	batch := len(aliases) > 1
	failed := runJobs(aliases, func(string) int64 { return size }, func(h string, j *job) error {
		return uploadTo(h, files, dest, batch, j)
	})
	if failed > 0 && batch && needsPrompt(aliases) {
		say(ui.Dim + "hosts that ask for a password: upload to them one at a time" + ui.Reset)
	}
	say()
	pause()
}

func downloadDir() string {
	if out, err := exec.Command("xdg-user-dir", "DOWNLOAD").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return d
		}
	}
	return filepath.Join(config.Home(), "Downloads")
}

func cmdDownload(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	var parent string
	var names []string
	if len(aliases) == 1 {
		picks := browse(aliases[0]+":", remoteLister(aliases[0]), ".", "alt-d")
		if len(picks) == 0 {
			return
		}
		parent = path.Dir(picks[0])
		for _, p := range picks {
			names = append(names, path.Base(p))
		}
		clear()
	} else {
		clear()
		fmt.Printf("%sdownload%s  %s← %s%s\n\n", cHi, ui.Reset, ui.Dim, strings.Join(aliases, " "), ui.Reset)
		p, ok := ask("remote file or folder (same path on every host)", "")
		if !ok || p == "" {
			return
		}
		p = strings.TrimRight(p, "/")
		if strings.HasPrefix(p, "~/") || path.IsAbs(p) {
			parent, names = path.Dir(p), []string{path.Base(p)}
		} else {
			parent, names = "~", []string{p} // relative to the remote home
		}
		say()
	}
	dl := downloadDir()
	open := dl
	if len(aliases) == 1 {
		open = filepath.Join(dl, aliases[0])
	}
	fmt.Printf("%sdownload%s  %s%s  →  %s%s\n", cHi, ui.Reset, ui.Dim, strings.Join(names, " "), open, ui.Reset)
	say()
	batch := len(aliases) > 1
	runJobs(aliases,
		func(h string) int64 { return remoteSize(h, parent, names, batch) },
		func(h string, j *job) error {
			dest := filepath.Join(dl, h)
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			err := downloadFrom(h, parent, names, dest, batch, j)
			if err != nil {
				os.Remove(dest) // only goes away if nothing arrived
			}
			return err
		})
	say()
	if confirm("open the folder?", true) {
		desktop.Detach("xdg-open", open)
	}
}

func cmdSftp(alias string) {
	info := hosts.Resolve(alias)
	// behind a jump host only the alias works (the file manager reads ~/.ssh/config);
	// otherwise the explicit address is the most portable form
	url := "sftp://" + info.Addr() + "/"
	if info.ProxyJump != "" {
		url = "sftp://" + alias + "/"
	}
	desktop.Notify("SFTP", "opening "+info.Addr()+" …")
	desktop.OpenFiles(set, url)
}

func cmdCopy(alias string) {
	addr := hosts.Resolve(alias).Addr()
	if desktop.Copy(addr) {
		desktop.Notify("ssh", "copied "+addr)
	} else {
		desktop.Notify("ssh", "no clipboard tool (wl-copy / xclip / xsel)")
	}
}
