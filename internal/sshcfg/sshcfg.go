// Package sshcfg reads and edits ~/.ssh/config.
//
// Host metadata lives in comments directly above a Host line, so the file stays
// a normal ssh config:
//
//	# group: Proxmox
//	# env: prod
//	# url: https://pm:8006
//	Host pm
package sshcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
)

// Host is one concrete (non-wildcard) alias from the config.
type Host struct {
	Alias string
	Group string
	Env   string
	URL   string
	File  string // file that defines it (the main config or an included one)
	Line  int    // 1-based line of its Host statement in File
}

// IsProd reports whether the host is marked "# env: prod".
func (h Host) IsProd() bool { return strings.EqualFold(h.Env, "prod") }

// Path is the main config file; tests point it elsewhere.
var Path = filepath.Join(config.Home(), ".ssh", "config")

// BackupDir receives a copy of a config file before every change.
var BackupDir = func() string { return config.State("backups") }

var (
	reMeta    = regexp.MustCompile(`(?i)^\s*#\s*(group|env|url)\s*:\s*(.+?)\s*$`)
	reHost    = regexp.MustCompile(`(?i)^\s*host\s+(.+?)\s*$`)
	reInclude = regexp.MustCompile(`(?i)^\s*include\s+(.+?)\s*$`)
	reStar    = regexp.MustCompile(`(?i)^\s*host\s+\*\s*$`)
	reWild    = regexp.MustCompile(`[*?!]`)
	// BadAlias matches characters an alias created by the menu must not contain.
	BadAlias = regexp.MustCompile(`[\s*?!]`)
)

func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}

// Files returns the main config and every file it includes (for cache invalidation).
func Files() []string {
	var out []string
	walk(Path, 0, map[string]bool{}, func(f string, _ []string) { out = append(out, f) })
	return out
}

// includeTargets expands the arguments of an Include line the way ssh does:
// relative paths are taken from ~/.ssh, "~" is the home directory, globs are allowed.
func includeTargets(arg string) []string {
	var out []string
	for _, p := range strings.Fields(arg) {
		p = strings.Trim(p, `"`)
		switch {
		case p == "~" || strings.HasPrefix(p, "~/"):
			p = filepath.Join(config.Home(), p[1:])
		case !filepath.IsAbs(p):
			p = filepath.Join(filepath.Dir(Path), p)
		}
		m, _ := filepath.Glob(p)
		sort.Strings(m)
		out = append(out, m...)
	}
	return out
}

// walk visits a config file and its includes in ssh's reading order.
func walk(path string, depth int, seen map[string]bool, visit func(file string, lines []string)) {
	if depth > 8 || seen[path] {
		return
	}
	seen[path] = true
	lines := readLines(path)
	if lines == nil {
		return
	}
	visit(path, lines)
	for _, l := range lines {
		if m := reInclude.FindStringSubmatch(l); m != nil {
			for _, inc := range includeTargets(m[1]) {
				walk(inc, depth+1, seen, visit)
			}
		}
	}
}

func parseFile(file string, lines []string) []Host {
	var hosts []Host
	meta := map[string]string{}
	for i, l := range lines {
		if m := reMeta.FindStringSubmatch(l); m != nil {
			meta[strings.ToLower(m[1])] = m[2]
			continue
		}
		if m := reHost.FindStringSubmatch(l); m != nil {
			for _, a := range strings.Fields(m[1]) {
				if !reWild.MatchString(a) {
					hosts = append(hosts, Host{Alias: a, Group: meta["group"], Env: meta["env"], URL: meta["url"], File: file, Line: i + 1})
				}
			}
			meta = map[string]string{}
		} else if strings.TrimSpace(l) == "" || !strings.HasPrefix(strings.TrimSpace(l), "#") {
			meta = map[string]string{}
		}
	}
	return hosts
}

// Parse returns all concrete hosts: the main config first, then included files.
// If an alias is defined twice, the first definition wins, as in ssh.
func Parse() []Host {
	var hosts []Host
	seen := map[string]bool{}
	walk(Path, 0, map[string]bool{}, func(file string, lines []string) {
		for _, h := range parseFile(file, lines) {
			if !seen[h.Alias] {
				seen[h.Alias] = true
				hosts = append(hosts, h)
			}
		}
	})
	return hosts
}

// Find returns the host with this alias, or nil.
func Find(alias string) *Host {
	for _, h := range Parse() {
		if h.Alias == alias {
			return &h
		}
	}
	return nil
}

// Groups lists the distinct group names, sorted case-insensitively.
func Groups() []string {
	set := map[string]bool{}
	var out []string
	for _, h := range Parse() {
		if h.Group != "" && !set[h.Group] {
			set[h.Group] = true
			out = append(out, h.Group)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func backup(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	dir := BackupDir()
	os.MkdirAll(dir, 0o700)
	name := "config-"
	if path != Path {
		name = filepath.Base(path) + "-"
	}
	os.WriteFile(filepath.Join(dir, name+time.Now().Format("20060102-150405")), b, 0o600)
	// keep the 20 newest copies of the main config
	old, _ := filepath.Glob(filepath.Join(dir, "config-*"))
	sort.Strings(old)
	for i := 0; i < len(old)-20; i++ {
		os.Remove(old[i])
	}
}

func writeLines(path string, lines []string) error {
	backup(path)
	text := strings.Join(lines, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	return config.WriteAtomic(path, []byte(text), 0o600)
}

func hostIndex(lines []string, alias string) int {
	for i, l := range lines {
		if m := reHost.FindStringSubmatch(l); m != nil {
			for _, a := range strings.Fields(m[1]) {
				if a == alias {
					return i
				}
			}
		}
	}
	return -1
}

func isComment(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "#") }

// SetMeta sets (or, with an empty value, removes) a "# key: value" comment above the host.
func SetMeta(alias, key, value string) error {
	h := Find(alias)
	if h == nil {
		return fmt.Errorf("%s: not found", alias)
	}
	lines := readLines(h.File)
	idx := hostIndex(lines, alias)
	if idx < 0 {
		return fmt.Errorf("%s: not found", alias)
	}
	j := idx
	for j > 0 && isComment(lines[j-1]) {
		j--
	}
	reKey := regexp.MustCompile(`(?i)^\s*#\s*` + regexp.QuoteMeta(key) + `\s*:`)
	var block []string
	for _, l := range lines[j:idx] {
		if !reKey.MatchString(l) {
			block = append(block, l)
		}
	}
	if value != "" {
		block = append(block, fmt.Sprintf("# %s: %s", key, value))
	}
	out := append([]string{}, lines[:j]...)
	out = append(out, block...)
	out = append(out, lines[idx:]...)
	return writeLines(h.File, out)
}

// ToggleProd flips the "# env: prod" mark.
func ToggleProd(alias string) error {
	h := Find(alias)
	if h == nil {
		return fmt.Errorf("%s: not found", alias)
	}
	if h.IsProd() {
		return SetMeta(alias, "env", "")
	}
	return SetMeta(alias, "env", "prod")
}

// NewHost describes a host to add; empty fields are left out.
type NewHost struct {
	Alias, HostName, User, Port, Jump, Group, Env, Identity string
}

// Add writes a new Host block to the main config, before the "Host *" defaults
// block (and the comments above it) so that the defaults stay last.
func Add(n NewHost) error {
	if n.Alias == "" || BadAlias.MatchString(n.Alias) || Find(n.Alias) != nil {
		return fmt.Errorf("bad or duplicate alias")
	}
	var block []string
	if n.Group != "" {
		block = append(block, "# group: "+n.Group)
	}
	if n.Env != "" {
		block = append(block, "# env: "+n.Env)
	}
	hostname := n.HostName
	if hostname == "" {
		hostname = n.Alias
	}
	block = append(block, "Host "+n.Alias, "    HostName "+hostname)
	if n.User != "" {
		block = append(block, "    User "+n.User)
	}
	if n.Port != "" && n.Port != "22" {
		block = append(block, "    Port "+n.Port)
	}
	if n.Jump != "" {
		block = append(block, "    ProxyJump "+n.Jump)
	}
	if n.Identity != "" {
		block = append(block, "    IdentityFile "+n.Identity, "    IdentitiesOnly yes")
	}

	lines := readLines(Path)
	star := -1
	for i, l := range lines {
		if reStar.MatchString(l) {
			star = i
			break
		}
	}
	if star >= 0 {
		j := star
		for j > 0 && isComment(lines[j-1]) {
			j--
		}
		for j > 0 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		out := append([]string{}, lines[:j]...)
		if j > 0 {
			out = append(out, "")
		}
		out = append(out, block...)
		out = append(out, lines[j:]...)
		return writeLines(Path, out)
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return writeLines(Path, append(lines, block...))
}
