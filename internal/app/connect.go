package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/desktop"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

// runTTY runs a program on the user's terminal and returns its exit code.
func runTTY(name string, args ...string) int {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 127
	}
	return 0
}

func output(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out))
}

// knownHostsName is how a host is written in known_hosts.
func knownHostsName(i hosts.Info) string {
	if i.Port == "22" || i.Port == "" {
		return i.HostName
	}
	return "[" + i.HostName + "]:" + i.Port
}

// fixHostKey explains a changed host key and offers to forget the old one.
// True when the old key was removed and connecting can go on.
func fixHostKey(alias string) bool {
	info := hosts.Resolve(alias)
	name := knownHostsName(info)
	fmt.Printf("\n%s⚠ The host key of %s has changed.%s\n", ui.CBad, alias, ui.Reset)
	say(ui.Dim + "Normal after a reinstall or a new VM on the same address." + ui.Reset)
	say(ui.Dim + "If you did not expect it, someone may be intercepting the connection: say no and check." + ui.Reset)
	say()
	if old := output("ssh-keygen", "-l", "-F", name); old != "" {
		say(ui.CLab + "known before:" + ui.Reset)
		for _, l := range strings.Split(old, "\n") {
			if !strings.HasPrefix(l, "#") {
				say("  " + l)
			}
		}
	}
	scan := exec.Command("sh", "-c", `ssh-keyscan -T 4 -p "$1" "$2" 2>/dev/null | ssh-keygen -lf - 2>/dev/null`, "sh", info.Port, info.HostName)
	if out, _ := scan.Output(); len(out) > 0 {
		say(ui.CLab + "offered now:" + ui.Reset)
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			say("  " + l)
		}
	}
	say()
	if !confirm("forget the old key and connect?", false) {
		return false
	}
	exec.Command("ssh-keygen", "-R", name).Run()
	return true
}

// runSSH is an interactive session: the window becomes a normal terminal, PROD gets a red tint.
func runSSH(target string, opts []string, remote ...string) int {
	hosts.Remember(target)
	if os.Getenv("SSH_MENU") == "1" {
		desktop.Unfloat() // a session, not a menu any more
	}
	h := sshcfg.Find(target)
	fixed := false // the stored host key was replaced: the audit cache must learn about it
	if h != nil && hosts.LoadAuth()[target].State == "hostkey" {
		if !fixHostKey(target) {
			return 1
		}
		fixed = true
	}
	args := append(append(append([]string{}, opts...), target), remote...)
	for attempt := 0; ; attempt++ {
		if h != nil && h.IsProd() {
			fmt.Printf("\x1b]2;⚠ PROD ssh: %s\a\x1b]11;#220a0f\a", target)
		} else {
			fmt.Printf("\x1b]2;ssh: %s\a", target)
		}
		start := time.Now()
		rc := runTTY("ssh", args...)
		fmt.Print("\x1b]111\a") // background colour back to normal
		// failed at once on a host that answers: maybe its key changed since the last check
		if rc == 255 && attempt == 0 && h != nil && time.Since(start) < 15*time.Second {
			if hosts.Probe(hosts.Resolve(target)) != "down" {
				if st, _ := hosts.CheckAuth(target); st == "hostkey" && fixHostKey(target) {
					fixed = true
					continue
				}
			}
		}
		if fixed {
			hosts.Audit([]string{target})
		}
		if rc != 0 {
			fmt.Printf("\n%sssh %s exited with %d%s  (press any key to close)", ui.Red, target, rc, ui.Reset)
			key()
			fmt.Println()
		}
		return rc
	}
}

// cmdConnect connects to the highlighted host, or to whatever was typed when nothing matches.
func cmdConnect(alias, query string) int {
	t := alias
	if t == "" {
		t = strings.TrimSpace(query)
	}
	if t == "" {
		return 0
	}
	return runSSH(t, nil)
}

// cmdTmux attaches to (or creates) the tmux session "main" on the host, falling back to a login shell.
func cmdTmux(alias string) int {
	return runSSH(alias, []string{"-t"}, `command -v tmux >/dev/null && exec tmux new-session -A -s main || exec $SHELL -l`)
}
