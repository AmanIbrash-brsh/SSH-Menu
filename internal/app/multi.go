package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "embed"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/desktop"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/fz"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

// ---------- run one command on several hosts ----------

const defaultSnippets = `# Saved commands for Alt+X, one per line. Lines starting with # are ignored.
# Alt+S in the command picker adds the command you typed.
uptime
df -h
free -h
systemctl --failed
docker ps
ss -tlnp
journalctl -p err -n 20 --no-pager
`

func snippetsPath() string { return filepath.Join(config.Dir(), "snippets") }
func cmdHistPath() string  { return config.State("cmd_history") }

func readLinesOf(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func appendLine(path, line string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// pickCommand lets the user type a command or take a saved / recent one.
func pickCommand(aliases []string) string {
	if _, err := os.Stat(snippetsPath()); os.IsNotExist(err) {
		os.MkdirAll(config.Dir(), 0o755)
		os.WriteFile(snippetsPath(), []byte(defaultSnippets), 0o644)
	}
	query := ""
	for {
		snips := readLinesOf(snippetsPath())
		seen := map[string]bool{}
		var items []string
		for _, s := range snips {
			seen[s] = true
			items = append(items, ui.CKey+"★"+ui.Reset+" "+s)
		}
		hist := readLinesOf(cmdHistPath())
		for i := len(hist) - 1; i >= 0 && len(items) < 200; i-- { // newest first, no repeats
			if !seen[hist[i]] {
				seen[hist[i]] = true
				items = append(items, ui.Dim+"·"+ui.Reset+" "+hist[i])
			}
		}
		header := ui.Keys("Enter", "run selected (or what you typed)", "Alt+Enter", "run exactly what you typed", "Tab", "edit selected", "Alt+S", "save as ★")
		out, code := picker(items, "$ ", "--print-query", "--exact", "--no-sort", "--query="+query,
			"--expect=alt-enter,alt-s", "--bind=tab:replace-query", "--header="+header,
			"--border-label= run on: "+strings.Join(aliases, " ")+" ", "--ghost=type a command, or pick a saved one")
		if code != fz.OK && code != fz.NoMatch {
			return ""
		}
		typed, keyName, sel := "", "", ""
		if len(out) > 0 {
			typed = strings.TrimSpace(out[0])
		}
		if len(out) > 1 {
			keyName = out[1]
		}
		if len(out) > 2 {
			_, sel, _ = strings.Cut(out[2], " ") // drop the ★ / · mark
		}
		switch keyName {
		case "alt-s":
			if c := typed; c != "" {
				appendLine(snippetsPath(), c)
			} else if sel != "" && !contains(snips, sel) {
				appendLine(snippetsPath(), sel)
			}
			query = typed
			continue
		case "alt-enter":
			return typed
		}
		if sel != "" {
			return sel
		}
		return typed
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

type runResult struct {
	host string
	rc   int
	out  string
}

func runOn(host, command string) runResult {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=6", host, command).CombinedOutput()
	rc := 0
	if err != nil {
		rc = -1
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		}
		if ctx.Err() != nil {
			return runResult{host, -1, "timeout"}
		}
	}
	return runResult{host, rc, strings.TrimRight(string(out), "\n")}
}

// cmdRun runs one command everywhere in parallel; hosts that answer the same are shown as one block.
func cmdRun(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	command := pickCommand(aliases)
	if command == "" {
		return
	}
	clear()
	fmt.Printf("%s$ %s%s\n%son: %s%s\n\n", cHi, command, ui.Reset, ui.Dim, strings.Join(aliases, " "), ui.Reset)
	if !confirmProd(aliases) {
		return
	}
	appendLine(cmdHistPath(), command)
	fmt.Printf("%srunning on %d host(s)…%s", ui.Dim, len(aliases), ui.Reset)
	res := hosts.Parallel(aliases, 16, func(h string) runResult { return runOn(h, command) })
	fmt.Print("\r\x1b[2K")

	type group struct {
		rc    int
		out   string
		hosts []string
	}
	var groups []*group
	for _, r := range res {
		var g *group
		for _, x := range groups {
			if x.rc == r.rc && x.out == r.out {
				g = x
			}
		}
		if g == nil {
			g = &group{rc: r.rc, out: r.out}
			groups = append(groups, g)
		}
		g.hosts = append(g.hosts, r.host)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s$ %s%s\n\n", ui.Dim, command, ui.Reset)
	for _, g := range groups {
		dot, note := ui.Green, ""
		if g.rc != 0 {
			dot = ui.Red
			note = fmt.Sprintf("  %sexit %d%s", ui.Dim, g.rc, ui.Reset)
			if g.rc == -1 || g.rc == 255 {
				note = "  " + ui.Dim + "ssh error" + ui.Reset
			}
		}
		fmt.Fprintf(&b, "%s●%s %s%s%s%s%s\n", dot, ui.Reset, ui.Bold, ui.CVal, strings.Join(g.hosts, ", "), ui.Reset, note)
		for _, l := range strings.Split(g.out, "\n") {
			if g.out != "" {
				b.WriteString("  " + l + "\n")
			}
		}
		b.WriteString("\n")
	}
	clear()
	if desktop.Have("less") {
		less := exec.Command("less", "-RFX")
		less.Stdin, less.Stdout, less.Stderr = strings.NewReader(b.String()), os.Stdout, os.Stderr
		less.Run()
	} else {
		fmt.Print(b.String())
	}
	pause()
}

// ---------- broadcast: one tmux pane per host, typing goes to all ----------

//go:embed tmux-broadcast.conf
var tmuxConf string

func cmdBroadcast(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	if !desktop.Have("tmux") {
		say(ui.Red + "broadcast needs tmux" + ui.Reset)
		pause()
		return
	}
	if !confirmProd(aliases) {
		return
	}
	conf := config.State("tmux-broadcast.conf")
	os.WriteFile(conf, []byte(tmuxConf), 0o644)
	// own tmux server (-L sshmenu): the user's ~/.tmux.conf and sessions are untouched
	T := []string{"tmux", "-L", "sshmenu", "-f", conf}
	tm := func(a ...string) { exec.Command(T[0], append(T[1:], a...)...).Run() }
	sess := "bc-" + time.Now().Format("150405")
	for i, h := range aliases {
		hosts.Remember(h)
		if i == 0 {
			tm("new-session", "-d", "-s", sess, "-x", "220", "-y", "60", "ssh "+fz.Quote(h))
		} else {
			tm("split-window", "-t", sess, "ssh "+fz.Quote(h))
			tm("select-layout", "-t", sess, "tiled")
		}
		tm("select-pane", "-t", sess, "-T", h)
	}
	tm("setw", "-t", sess, "synchronize-panes", "on")
	argv := desktop.TerminalCommand(set, fmt.Sprintf("broadcast: %d hosts", len(aliases)), "ssh-menu-broadcast", append(T, "attach", "-t", sess))
	if argv == nil {
		say(ui.Red + "no terminal emulator found: set SSHM_TERMINAL" + ui.Reset)
		pause()
		return
	}
	desktop.Detach(argv...)
}

// ---------- push an SSH key ----------

func pickKey() string {
	pubs, _ := filepath.Glob(filepath.Join(config.Home(), ".ssh", "*.pub"))
	const gen = "+ generate new ed25519 key"
	out, code := picker(append(pubs, gen), "key ❯ ")
	if code != fz.OK || len(out) == 0 {
		return ""
	}
	if out[0] != gen {
		return out[0]
	}
	clear()
	name, ok := ask("key file name", "id_ed25519_"+time.Now().Format("0601"))
	if !ok || name == "" {
		return ""
	}
	path := filepath.Join(config.Home(), ".ssh", name)
	if _, err := os.Stat(path); err == nil {
		say(ui.Red + name + " exists" + ui.Reset)
		pause()
		return ""
	}
	host, _ := os.Hostname()
	if runTTY("ssh-keygen", "-t", "ed25519", "-f", path, "-C", os.Getenv("USER")+"@"+host) != 0 {
		return ""
	}
	return path + ".pub"
}

// pushKey installs a public key on a host: ssh-copy-id when it exists, otherwise the same thing by hand.
func pushKey(pub, host string) bool {
	if desktop.Have("ssh-copy-id") {
		return runTTY("ssh-copy-id", "-i", pub, host) == 0
	}
	keyText, err := os.ReadFile(pub)
	if err != nil {
		return false
	}
	k := fz.Quote(strings.TrimSpace(string(keyText)))
	remote := "umask 077; mkdir -p ~/.ssh && { grep -qxF " + k + " ~/.ssh/authorized_keys 2>/dev/null || echo " + k + " >> ~/.ssh/authorized_keys; }"
	return runTTY("ssh", host, remote) == 0
}

func cmdKeys(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	pub := pickKey()
	if pub == "" {
		return
	}
	clear()
	fmt.Printf("%skey:%s %s  %s→%s %s\n\n", cHi, ui.Reset, pub, cHi, ui.Reset, strings.Join(aliases, " "))
	var ok, bad []string
	for _, h := range aliases {
		fmt.Printf("%s── %s%s\n", cHi, h, ui.Reset)
		if pushKey(pub, h) {
			ok = append(ok, h)
		} else {
			bad = append(bad, h)
		}
	}
	say()
	if len(ok) > 0 {
		say(okLine(strings.Join(ok, " ")))
		hosts.Audit(ok) // refresh the "password / no access" marks
	}
	if len(bad) > 0 {
		say(failLine(strings.Join(bad, " ")))
	}
	pause()
}
