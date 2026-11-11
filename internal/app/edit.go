package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/desktop"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/fz"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

// cmdNew is the new-host wizard. The alias of the created host is left in
// state/lastnew so the menu can jump to it.
func cmdNew() {
	clear()
	fmt.Printf("%s── new SSH host ──%s  (Enter = default, Ctrl+C = cancel)\n\n", cHi, ui.Reset)
	var n sshcfg.NewHost
	for {
		a, ok := ask("alias (name in the menu)", "")
		if !ok || a == "" {
			return
		}
		if sshcfg.Find(a) != nil {
			say(ui.Red + "already exists" + ui.Reset)
			continue
		}
		if sshcfg.BadAlias.MatchString(a) {
			say(ui.Red + "no spaces or wildcards" + ui.Reset)
			continue
		}
		n.Alias = a
		break
	}
	steps := []struct {
		dst         *string
		prompt, def string
	}{
		{&n.HostName, "HostName / IP", n.Alias},
		{&n.User, "User", "root"},
		{&n.Port, "Port", "22"},
		{&n.Jump, "ProxyJump (empty = none)", ""},
		{&n.Group, "group (empty = none)", ""},
	}
	for _, s := range steps {
		v, ok := ask(s.prompt, s.def)
		if !ok {
			return
		}
		*s.dst = v
	}
	if confirm("mark as PROD (red window)?", false) {
		n.Env = "prod"
	}
	id, ok := ask("IdentityFile (empty = default)", "")
	if !ok {
		return
	}
	n.Identity = id

	via, grp, prod := "", "", ""
	if n.Jump != "" {
		via = "via " + n.Jump + " "
	}
	if n.Group != "" {
		grp = "[" + n.Group + "] "
	}
	if n.Env != "" {
		prod = "PROD"
	}
	fmt.Printf("\n%sHost %s → %s@%s:%s %s%s%s%s\n", ui.Dim, n.Alias, n.User, n.HostName, n.Port, via, grp, prod, ui.Reset)
	if !confirm("save to ~/.ssh/config?", true) {
		return
	}
	if err := sshcfg.Add(n); err != nil {
		say(ui.Red + err.Error() + ui.Reset)
		pause()
		return
	}
	say(okLine("saved") + ui.Dim + " (backup in " + config.State("backups") + ")" + ui.Reset)
	os.WriteFile(config.State("lastnew"), []byte(n.Alias+"\n"), 0o600)
	if confirm("push your public key to it now?", true) {
		cmdKeys([]string{n.Alias})
	}
}

// cmdEdit opens the host's block in an editor.
func cmdEdit(alias string) {
	ed := set.Editor
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		for _, e := range []string{"micro", "nano", "vim", "vi"} {
			if desktop.Have(e) {
				ed = e
				break
			}
		}
	}
	if ed == "" {
		say(ui.Red + "no editor found: set SSHM_EDITOR" + ui.Reset)
		pause()
		return
	}
	file, line := sshcfg.Path, 1
	if h := sshcfg.Find(alias); h != nil {
		file, line = h.File, h.Line
	}
	argv := append(strings.Fields(ed), "+"+strconv.Itoa(line), file) // +LINE: micro, nano, vim and most others
	runTTY(argv[0], argv[1:]...)
}

// cmdGroup sets (or clears) the group of the given hosts.
func cmdGroup(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	const none = "(no group)"
	out, code := picker(append([]string{none}, sshcfg.Groups()...), "group ❯ ", "--print-query",
		"--header="+ui.Keys("Enter", "pick a group, or use the new name you typed"))
	if code != fz.OK && code != fz.NoMatch {
		return // Esc
	}
	g := ""
	if len(out) > 1 {
		g = out[1] // a listed group was chosen
	} else if len(out) == 1 {
		g = strings.TrimSpace(out[0]) // nothing matched: the typed name
	}
	if g == none {
		g = ""
	}
	for _, a := range aliases {
		sshcfg.SetMeta(a, "group", g)
	}
}

func cmdProd(aliases []string) {
	for _, a := range aliases {
		sshcfg.ToggleProd(a)
	}
}

// urlDefault guesses a web UI address for the first-time question.
func urlDefault(alias string) string {
	info, h := hosts.Resolve(alias), sshcfg.Find(alias)
	port := ""
	if h != nil && strings.EqualFold(h.Group, "proxmox") {
		port = ":8006"
		if alias == "pbs" {
			port = ":8007"
		}
	}
	return "https://" + info.HostName + port
}

// cmdWeb opens the host's web UI; the first time it asks for the address and stores it as "# url:".
func cmdWeb(alias string) {
	url := ""
	if h := sshcfg.Find(alias); h != nil {
		url = h.URL
	}
	if url == "" {
		u, ok := ask("web UI for "+alias, urlDefault(alias))
		if !ok || u == "" {
			return
		}
		url = u
		sshcfg.SetMeta(alias, "url", url)
	}
	desktop.OpenURL(set, url)
}
