// Package ui renders everything the menu shows: list rows, the preview panel,
// the key help and the footer.
package ui

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/hosts"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/stats"
)

// ---------- colours ----------

const (
	Reset = "\x1b[0m"
	Dim   = "\x1b[2m"
	Bold  = "\x1b[1m"
	Green = "\x1b[32m"
	Red   = "\x1b[31m"
)

func rgb(r, g, b int) string     { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b) }
func rgbBold(r, g, b int) string { return fmt.Sprintf("\x1b[1;38;2;%d;%d;%dm", r, g, b) }

var (
	CLab  = rgb(120, 108, 112) // labels, secondary text
	CVal  = rgb(235, 225, 230) // values
	CSoft = rgb(205, 195, 200)
	COk   = rgb(120, 235, 90)
	CWarn = rgb(224, 175, 104)
	CBad  = rgb(255, 92, 108)
	CAcc  = rgbBold(233, 30, 99)   // accent (pink)
	CKey  = rgbBold(248, 187, 217) // hotkeys
	CHint = rgb(150, 135, 140)
	CProd = rgbBold(255, 92, 108)
	cRail = rgb(70, 58, 62) // empty part of a bar
)

var palette = [][3]int{{248, 187, 217}, {125, 207, 255}, {158, 206, 106}, {224, 175, 104}, {187, 154, 247}, {115, 218, 202}}
var fixedGroups = map[string][3]int{"proxmox": {248, 187, 217}, "services": {125, 207, 255}, "git": {158, 206, 106}, "legacy": {130, 120, 125}}

// GroupColor gives every group a stable colour.
func GroupColor(name string) string {
	n := strings.ToLower(name)
	c, ok := fixedGroups[n]
	if !ok {
		c = palette[crc32.ChecksumIEEE([]byte(n))%uint32(len(palette))]
	}
	return rgb(c[0], c[1], c[2])
}

func pad(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// ---------- list ----------

var reAddr = regexp.MustCompile(`^(.*)@(.*):(\d+)$`)

// addrCell is host[:port] (the user is shown in the preview), padded to width.
func addrCell(addr string, width int) string {
	m := reAddr.FindStringSubmatch(addr)
	if m == nil {
		return Dim + pad(addr, width) + Reset
	}
	host, port := m[2], ""
	if m[3] != "22" {
		port = ":" + m[3]
	}
	return CSoft + host + Reset + CLab + port + Reset + strings.Repeat(" ", max(0, width-len(host)-len(port)))
}

// authTag marks hosts where key login does not work; nothing is shown when it does.
var authTag = map[string][2]string{
	"pw":      {CWarn, "password"},
	"nokey":   {CBad, "no access"},
	"hostkey": {CBad, "host key changed"},
	"newkey":  {CLab, "new host key"},
}

// List writes the host rows. Field 2 of every row is the alias (the menu relies on it).
func List(w io.Writer) {
	hs := hosts.LoadFilter().Apply(hosts.Sorted())
	status, auth := hosts.LoadStatus(), hosts.LoadAuth()
	aw, gw := 8, 0 // column widths follow the content
	for _, h := range hs {
		aw, gw = max(aw, len(h.Alias)), max(gw, len(h.Group))
	}
	var b strings.Builder
	for _, h := range hs {
		st := status[h.Alias]
		dot := Dim + "·" + Reset
		switch st.State {
		case "up":
			dot = Green + "●" + Reset
		case "down":
			dot = "\x1b[1;31m●" + Reset
		case "jump":
			dot = "\x1b[33m●" + Reset
		}
		group := strings.Repeat(" ", gw)
		if h.Group != "" {
			group = GroupColor(h.Group) + pad(h.Group, gw) + Reset
		}
		prod := "    "
		if h.IsProd() {
			prod = CProd + "PROD" + Reset
		}
		tag := ""
		if t, ok := authTag[auth[h.Alias].State]; ok {
			tag = t[0] + t[1] + Reset
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s   %s  %s\n", dot, pad(h.Alias, aw), group, prod, addrCell(st.Addr, 16), tag)
	}
	io.WriteString(w, b.String())
}

// ---------- preview ----------

func lab(t string) string { return CLab + pad(t, 8) + Reset }
func val(t string) string { return CVal + t + Reset }

func humanKiB(k int64) string {
	g := float64(k) / 1048576
	switch {
	case g < 10:
		return fmt.Sprintf("%.1fG", g)
	case g < 1000:
		return fmt.Sprintf("%.0fG", g)
	}
	return fmt.Sprintf("%.1fT", g/1024)
}

func humanSecs(s int64) string {
	d, h, m := s/86400, s%86400/3600, s%3600/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func ago(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return "just now"
	case s < 3600:
		return fmt.Sprintf("%d min ago", s/60)
	}
	return fmt.Sprintf("%d h ago", s/3600)
}

func level(pct float64, normal string) string {
	switch {
	case pct >= 90:
		return CBad
	case pct >= 80:
		return CWarn
	}
	return normal
}

// Bar is a 10-cell usage bar that turns amber at 80% and red at 90%.
func Bar(pct float64) string {
	const width = 10
	n := min(width, int(pct/100*width+0.5))
	return level(pct, CSoft) + strings.Repeat("━", n) + cRail + strings.Repeat("━", width-n) + Reset
}

func pctCell(pct float64) string { return fmt.Sprintf("%s%3.0f%%%s", level(pct, CVal), pct, Reset) }

var reSkipMount = regexp.MustCompile(`^/(run|snap|var/lib/docker|var/snap)(/|$)`)

// Stats renders a snapshot as lines for the preview panel.
func Stats(d *stats.Data) []string {
	var out []string
	if d.OS != "" {
		out = append(out, rgbBold(205, 195, 200)+d.OS+Reset)
	}
	if d.Up > 0 {
		out = append(out, lab("up")+val(humanSecs(d.Up)))
	}
	if d.Load != "" {
		var l1 float64
		fmt.Sscanf(d.Load, "%f", &l1)
		col := CVal
		if d.CPU > 0 && l1 > float64(2*d.CPU) {
			col = CBad
		} else if d.CPU > 0 && l1 > float64(d.CPU) {
			col = CWarn
		}
		line := lab("load") + col + d.Load + Reset
		if d.CPU > 0 {
			line += fmt.Sprintf("%s  · %d cpu%s", CLab, d.CPU, Reset)
		}
		out = append(out, line)
	}
	if d.MemTotal > 0 {
		pct := float64(d.MemUsed) * 100 / float64(d.MemTotal)
		out = append(out, lab("mem")+Bar(pct)+" "+pctCell(pct)+fmt.Sprintf("%s  %s / %s%s", CLab, humanKiB(d.MemUsed), humanKiB(d.MemTotal), Reset))
	}
	type disk struct {
		mount string
		pct   float64
		free  int64
	}
	var disks []disk
	for _, x := range d.Disks {
		if x.Total == 0 || reSkipMount.MatchString(x.Mount) {
			continue
		}
		pct := float64(x.Used) * 100 / float64(x.Total)
		if strings.HasPrefix(x.Mount, "/boot") && pct < 80 {
			continue
		}
		disks = append(disks, disk{x.Mount, pct, x.Total - x.Used})
	}
	// "/" first, then the fullest
	sort.SliceStable(disks, func(i, j int) bool {
		if (disks[i].mount == "/") != (disks[j].mount == "/") {
			return disks[i].mount == "/"
		}
		return disks[i].pct > disks[j].pct
	})
	for i, x := range disks {
		if i == 4 {
			out = append(out, lab("")+CLab+fmt.Sprintf("+%d more", len(disks)-4)+Reset)
			break
		}
		name := ""
		if i == 0 {
			name = "disk"
		}
		extra := ""
		if x.pct >= 80 {
			extra = fmt.Sprintf("%s  %s free%s", CWarn, humanKiB(x.free), Reset)
		}
		out = append(out, lab(name)+Bar(x.pct)+" "+pctCell(x.pct)+"  "+val(x.mount)+extra)
	}
	if d.HasUpd {
		line := CLab + "up to date" + Reset
		if d.Upd > 0 {
			line = fmt.Sprintf("%s%d pending%s", CWarn, d.Upd, Reset)
			if d.UpdSec > 0 {
				line += fmt.Sprintf("%s  · %d security%s", CBad, d.UpdSec, Reset)
			}
		}
		out = append(out, lab("updates")+line)
	}
	if d.Reboot {
		out = append(out, lab("")+CWarn+"reboot required"+Reset)
	}
	if n := len(d.Failed); n > 0 {
		s := "s"
		if n == 1 {
			s = ""
		}
		out = append(out, lab("failed")+fmt.Sprintf("%s%d unit%s%s", CBad, n, s, Reset))
		for i, u := range d.Failed {
			if i == 4 {
				break
			}
			out = append(out, lab("")+CLab+u+Reset)
		}
	} else if d.HasUnits {
		out = append(out, lab("units")+CLab+"all running"+Reset)
	}
	if d.HasDock && d.Docker > 0 {
		out = append(out, lab("docker")+val(fmt.Sprintf("%d running", d.Docker)))
	}
	return out
}

var authText = map[string][3]string{
	"pw":      {CWarn, "password only", "Alt+K to push key"},
	"nokey":   {CBad, "key rejected", "Alt+K to push key"},
	"hostkey": {CBad, "host key CHANGED", "Enter offers a fix"},
	"newkey":  {CWarn, "host key not known yet", "connect once to accept"},
}

var rePing = regexp.MustCompile(`time=([\d.]+)`)

func ping(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ping", "-c1", "-W1", host).Output()
	if m := rePing.FindSubmatch(out); m != nil {
		return val(string(m[1]) + " ms")
	}
	if err != nil && len(out) == 0 {
		return CLab + "-" + Reset
	}
	return CBad + "no reply" + Reset
}

// Preview writes the side panel for a host. What is known is printed at once;
// when the cached system info is old it is shown first and then replaced.
func Preview(w io.Writer, alias string) {
	h := sshcfg.Find(alias)
	if h == nil {
		fmt.Fprintf(w, "%s\n\n%snot in ~/.ssh/config:\nwill connect as typed%s\n", alias, Dim, Reset)
		return
	}
	info := hosts.Resolve(alias)
	stCh, pingCh := make(chan string, 1), make(chan string, 1)
	go func() { stCh <- hosts.Probe(info) }()
	go func() {
		if info.ProxyJump != "" {
			pingCh <- ""
			return
		}
		pingCh <- ping(info.HostName)
	}()
	st, pingText := <-stCh, <-pingCh
	auth, audited := hosts.LoadAuth()[alias]

	var head strings.Builder
	col := map[string]string{"up": COk, "down": CBad, "jump": CWarn}[st]
	prod := ""
	if h.IsProd() {
		prod = "  " + CProd + "PROD" + Reset
	}
	fmt.Fprintf(&head, "%s%s● %s%s%s\n%s%s%s\n\n", Bold, col, alias, Reset, prod, CLab, info.Addr(), Reset)
	if h.Group != "" {
		head.WriteString(lab("group") + GroupColor(h.Group) + h.Group + Reset + "\n")
	}
	if info.ProxyJump != "" {
		head.WriteString(lab("via") + val(info.ProxyJump) + "\n")
	}
	switch t, bad := authText[auth.State]; {
	case audited && auth.State == "key":
		k := auth.Key
		if k == "" {
			k = "agent"
		}
		head.WriteString(lab("key") + val(k) + "\n")
	case audited && bad:
		head.WriteString(lab("key") + t[0] + t[1] + Reset + CLab + "  · " + t[2] + Reset + "\n")
	default:
		k := "default / agent"
		if info.Identity != "" {
			k = filepath.Base(info.Identity)
		}
		head.WriteString(lab("key") + val(k) + "\n")
	}
	if h.URL != "" {
		head.WriteString(lab("web") + val(h.URL) + "\n")
	}
	if pingText != "" {
		head.WriteString(lab("ping") + pingText + "\n")
	}
	head.WriteString("\n")
	io.WriteString(w, head.String())

	if st == "down" {
		fmt.Fprintln(w, CBad+"unreachable"+Reset)
		return
	}
	if audited && auth.State != "key" {
		fmt.Fprintln(w, CLab+"system info needs key login"+Reset)
		return
	}
	body := func(d *stats.Data, note string) string {
		if d.NoShell {
			return CLab + "no shell here (git / network device)" + Reset + "\n"
		}
		return strings.Join(Stats(d), "\n") + fmt.Sprintf("\n\n%s%s · %s%s\n", CLab, ago(d.Age()), note, Reset)
	}
	old := stats.Load(alias)
	if old != nil && old.Fresh() {
		io.WriteString(w, body(old, "Ctrl+R to refresh"))
		return
	}
	if old != nil {
		io.WriteString(w, body(old, "updating…"))
	}
	fresh := stats.Fetch(alias, old != nil && !old.NoShell)
	switch {
	case fresh != nil && old != nil:
		io.WriteString(w, "\x1b[2J"+head.String()+body(fresh, "Ctrl+R to refresh")) // replace the stale panel
	case fresh != nil:
		io.WriteString(w, body(fresh, "Ctrl+R to refresh"))
	case old != nil:
		io.WriteString(w, "\x1b[2J"+head.String()+body(old, "update failed"))
	default:
		fmt.Fprintln(w, CLab+"no system info (login failed)"+Reset)
	}
}

// ---------- help and footer ----------

// Help writes the full key reference for the side panel.
func Help(w io.Writer, hasTmux bool) {
	var b strings.Builder
	sec := func(t string) { b.WriteString(CAcc + t + Reset + "\n") }
	k := func(key, txt string) { fmt.Fprintf(&b, "  %s%s%s%s%s\n", CKey, pad(key, 10), Reset, CVal, txt+Reset) }
	sec("connect")
	k("Enter", "ssh")
	k("Alt+T", "ssh + tmux session")
	k("Alt+F", "files in file manager")
	k("Alt+U", "upload")
	k("Alt+D", "download")
	k("Alt+W", "open web UI")
	k("Alt+Y", "copy user@host")
	b.WriteString("\n")
	sec("several hosts")
	k("Tab", "mark · Alt+A/Z all/none")
	k("Alt+X", "run a command on all")
	if hasTmux {
		k("Alt+B", "broadcast: type into all")
	}
	k("Alt+K", "push SSH key")
	b.WriteString("\n")
	sec("list")
	k("Alt+← →", "show one group")
	k("Alt+P", "all / no PROD / PROD only")
	k("Ctrl+R", "recheck status, keys, info")
	b.WriteString("\n")
	sec("edit")
	k("Alt+N", "new host")
	k("Alt+E", "edit in ~/.ssh/config")
	k("Alt+G", "set group")
	k("Alt+R", "mark / unmark PROD")
	io.WriteString(w, b.String())
}

// Keys renders "key description" pairs as one quiet line (footers and headers).
func Keys(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString(CKey + pairs[i] + Reset + " " + CHint + pairs[i+1] + Reset + "   ")
	}
	return b.String()
}
