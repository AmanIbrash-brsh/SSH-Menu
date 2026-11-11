// Package app is the ssh-menu program: the menu, its actions and the command line.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/desktop"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/stats"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

var (
	set  config.Settings
	self string // path of this program, for the commands bound to keys
)

const usage = `SSH Menu: pick a server from ~/.ssh/config and connect.

  ssh-menu           open the menu in this terminal
  ssh-menu popup     open the menu in its own terminal window (bind this to a hotkey)
  ssh-menu doctor    check what is installed and what each missing tool disables
  ssh-menu version   print the version

Keys inside the menu: press Alt+H.
Settings: ~/.config/ssh-menu/config   Saved commands: ~/.config/ssh-menu/snippets
`

// Main runs the program and returns its exit code.
func Main(version string, args []string) int {
	set = config.Load()
	self, _ = os.Executable()
	if self == "" {
		self = os.Args[0]
	}
	if len(args) == 0 {
		if _, err := exec.LookPath("ssh"); err != nil {
			fmt.Fprintln(os.Stderr, "ssh-menu needs ssh (package openssh-clients / openssh-client)")
			return 1
		}
		config.EnsureFile()
		return menu()
	}
	cmd, rest := args[0], args[1:]
	arg := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	switch cmd {
	case "popup":
		return popup()
	case "doctor":
		return doctor(version)
	case "version", "--version", "-v":
		fmt.Println("ssh-menu", version)
	case "help", "--help", "-h":
		fmt.Print(usage)

	// ---- internal: what the menu's keys run ----
	case "_list":
		ui.List(os.Stdout)
	case "_live":
		hosts.Refresh(false)
		ui.List(os.Stdout)
	case "_refresh":
		stats.Forget()
		hosts.Refresh(true)
		ui.List(os.Stdout)
	case "_preview":
		if _, err := os.Stat(helpFlag()); err == nil {
			ui.Help(os.Stdout, desktop.Have("tmux"))
		} else {
			ui.Preview(os.Stdout, arg(0))
		}
	case "_help": // toggle the key reference in the side panel; prints the new panel title
		if _, err := os.Stat(helpFlag()); err == nil {
			os.Remove(helpFlag())
			fmt.Println(" details ")
		} else {
			os.WriteFile(helpFlag(), nil, 0o600)
			fmt.Println(" keys ")
		}
	case "_filter":
		hosts.StepFilter(arg(0))
	case "_label":
		fmt.Println(hosts.Label())
	case "_connect":
		return cmdConnect(arg(0), arg(1))
	case "_tmux":
		return cmdTmux(arg(0))
	case "_sftp":
		cmdSftp(arg(0))
	case "_copy":
		cmdCopy(arg(0))
	case "_web":
		cmdWeb(arg(0))
	case "_edit":
		cmdEdit(arg(0))
	case "_new":
		cmdNew()
	case "_keys":
		cmdKeys(rest)
	case "_group":
		cmdGroup(rest)
	case "_prod":
		cmdProd(rest)
	case "_upload":
		cmdUpload(rest)
	case "_download":
		cmdDownload(rest)
	case "_run":
		cmdRun(rest)
	case "_broadcast":
		cmdBroadcast(rest)
	default:
		fmt.Fprintf(os.Stderr, "ssh-menu: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	return 0
}

// popup opens the menu in its own terminal window.
func popup() int {
	argv := desktop.TerminalCommand(set, "ssh menu", "ssh-menu", []string{self})
	if argv == nil {
		fmt.Fprintln(os.Stderr, "ssh-menu: no terminal emulator found, set SSHM_TERMINAL in "+config.File())
		return 1
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh-menu: terminal %q not found\n", argv[0])
		return 1
	}
	// SSH_MENU=1 tells the menu it owns the window (it is re-tiled when a session starts)
	err = syscall.Exec(path, argv, desktop.CleanEnv("SSH_MENU=1"))
	fmt.Fprintln(os.Stderr, "ssh-menu:", err)
	return 1
}

// doctor reports what works on this machine.
func doctor(version string) int {
	fmt.Println("ssh-menu", version)
	fmt.Println()
	bad := 0
	check := func(ok bool, name, note string) {
		if ok {
			fmt.Println(okLine(name))
			return
		}
		fmt.Println(ui.CWarn + "!" + ui.Reset + " " + name + ui.Dim + "  " + note + ui.Reset)
	}
	need := func(prog, note string) {
		if desktop.Have(prog) {
			fmt.Println(okLine(prog))
			return
		}
		bad++
		fmt.Println(failLine(prog) + ui.Dim + "  " + note + ui.Reset)
	}
	need("ssh", "required: install openssh-clients / openssh-client")
	check(desktop.Have("tar"), "tar", "needed on servers too: Alt+U / Alt+D send files as a tar stream")
	check(desktop.Have("tmux"), "tmux", "without it Alt+B (broadcast) is unavailable")
	check(desktop.Have("ssh-copy-id"), "ssh-copy-id", "not needed: Alt+K has its own way to install a key")
	clip := desktop.Have("wl-copy") || desktop.Have("xclip") || desktop.Have("xsel")
	check(clip, "clipboard (wl-copy / xclip / xsel)", "without it Alt+Y cannot copy the address")
	check(desktop.Have("zenity") || desktop.Have("kdialog"), "file dialog (zenity / kdialog)", "optional: the built-in file browser is used instead")
	check(desktop.Have("notify-send"), "notify-send", "optional: no desktop notifications")
	check(desktop.Have("ping"), "ping", "optional: no latency in the side panel")
	t := desktop.Terminal(set)
	check(t != "" && desktop.Have(t), "terminal for the popup: "+orNone(t), "set SSHM_TERMINAL in "+config.File())
	fmt.Println()

	hs := sshcfg.Parse()
	fmt.Printf("%s  %d hosts in %s", okLine("config"), len(hs), sshcfg.Path)
	if n := len(sshcfg.Files()) - 1; n > 0 {
		fmt.Printf(" (+%d included file(s))", n)
	}
	fmt.Println()
	mux := strings.Contains(output("ssh", "-G", "ssh-menu-doctor-probe"), "controlmaster auto")
	check(mux, "connection sharing (ControlMaster auto)", "recommended in ~/.ssh/config under Host *: makes everything instant, see README")
	fmt.Println(ui.Dim + "settings: " + config.File() + "   state: " + config.StateDir() + ui.Reset)
	if bad > 0 {
		return 1
	}
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "none found"
	}
	return s
}
