// Package hosts answers questions about the hosts in ~/.ssh/config:
// how ssh would reach them, whether they are up, and whether key login works.
// Every answer is cached in the state directory, so the menu opens instantly.
package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
)

const (
	AuthTTL      = 12 * time.Hour         // key login is re-checked twice a day
	probeTimeout = 600 * time.Millisecond // TCP connect to the ssh port
)

// ---------- how ssh resolves a host (ssh -G), cached until the config changes ----------

// Info is what `ssh -G alias` says about a host.
type Info struct {
	User      string `json:"user"`
	HostName  string `json:"hostname"`
	Port      string `json:"port"`
	ProxyJump string `json:"proxyjump,omitempty"`
	Identity  string `json:"identity,omitempty"` // first identity file that exists
}

// Addr is user@host:port.
func (i Info) Addr() string { return fmt.Sprintf("%s@%s:%s", i.User, i.HostName, i.Port) }

type resolveCache struct {
	Stamp string          `json:"stamp"`
	Hosts map[string]Info `json:"hosts"`
}

// stamp changes whenever the config (or a file it includes) changes.
func stamp() string {
	var b strings.Builder
	for _, f := range sshcfg.Files() {
		if st, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", f, st.ModTime().UnixNano(), st.Size())
		}
	}
	return b.String()
}

func resolveOne(alias string) Info {
	info := Info{HostName: alias, Port: "22"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-G", alias).Output()
	if err != nil {
		return info
	}
	var ids []string
	for _, l := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(l, " ")
		switch k {
		case "user":
			info.User = v
		case "hostname":
			info.HostName = v
		case "port":
			info.Port = v
		case "proxyjump":
			info.ProxyJump = v
		case "identityfile":
			if strings.HasPrefix(v, "~/") {
				v = filepath.Join(config.Home(), v[2:])
			}
			ids = append(ids, v)
		}
	}
	for _, p := range ids {
		if _, err := os.Stat(p); err == nil {
			info.Identity = p
			break
		}
	}
	return info
}

func resolvePath() string { return config.State("resolve.json") }

// ResolveAll returns ssh's view of the given aliases. `ssh -G` runs only for
// aliases that are not cached for the current version of the config.
func ResolveAll(aliases []string) map[string]Info {
	var c resolveCache
	st := stamp()
	if b, err := os.ReadFile(resolvePath()); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Stamp != st || c.Hosts == nil {
		c = resolveCache{Stamp: st, Hosts: map[string]Info{}}
	}
	var missing []string
	for _, a := range aliases {
		if _, ok := c.Hosts[a]; !ok {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		res := parallel(missing, 32, resolveOne)
		for i, a := range missing {
			c.Hosts[a] = res[i]
		}
		if b, err := json.Marshal(c); err == nil {
			config.WriteAtomic(resolvePath(), b, 0o600)
		}
	}
	out := make(map[string]Info, len(aliases))
	for _, a := range aliases {
		out[a] = c.Hosts[a]
	}
	return out
}

// Resolve is ResolveAll for one alias.
func Resolve(alias string) Info { return ResolveAll([]string{alias})[alias] }

// ForgetResolved drops the ssh -G cache (Ctrl+R).
func ForgetResolved() { os.Remove(resolvePath()) }

// parallel maps f over items with at most n goroutines, keeping the order.
func parallel[T, R any](items []T, n int, f func(T) R) []R {
	out := make([]R, len(items))
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			out[i] = f(it)
			<-sem
		}()
	}
	wg.Wait()
	return out
}

// Parallel is exported for other packages that fan work out over hosts.
func Parallel[T, R any](items []T, n int, f func(T) R) []R { return parallel(items, n, f) }

// ---------- reachability ----------

// Probe returns "up", "down", or "jump" (behind a ProxyJump: not checked directly).
func Probe(i Info) string {
	if i.ProxyJump != "" {
		return "jump"
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(i.HostName, i.Port), probeTimeout)
	if err != nil {
		return "down"
	}
	c.Close()
	return "up"
}

// Status is the cached reachability of a host.
type Status struct{ Addr, State string }

func statusPath() string { return config.State("status.tsv") }

// LoadStatus reads the last known reachability of every host.
func LoadStatus() map[string]Status {
	m := map[string]Status{}
	b, err := os.ReadFile(statusPath())
	if err != nil {
		return m
	}
	for _, l := range strings.Split(string(b), "\n") {
		if p := strings.Split(l, "\t"); len(p) == 3 {
			m[p[0]] = Status{p[1], p[2]}
		}
	}
	return m
}

func saveStatus(order []string, m map[string]Status) {
	var b strings.Builder
	for _, a := range order {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", a, m[a].Addr, m[a].State)
	}
	config.WriteAtomic(statusPath(), []byte(b.String()), 0o600)
}

// ---------- key login audit ----------

// Auth is the cached answer to "does public key login work here?".
// State: key | pw | nokey | hostkey | newkey | down.
type Auth struct {
	State string
	Key   string // key file the server accepted
	TS    int64
}

func authPath() string { return config.State("auth.tsv") }

// LoadAuth reads the audit cache.
func LoadAuth() map[string]Auth {
	m := map[string]Auth{}
	b, err := os.ReadFile(authPath())
	if err != nil {
		return m
	}
	for _, l := range strings.Split(string(b), "\n") {
		if p := strings.Split(l, "\t"); len(p) == 4 {
			ts, _ := strconv.ParseFloat(p[3], 64)
			m[p[0]] = Auth{p[1], p[2], int64(ts)}
		}
	}
	return m
}

func saveAuth(m map[string]Auth) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\n", k, m[k].State, m[k].Key, m[k].TS)
	}
	config.WriteAtomic(authPath(), []byte(b.String()), 0o600)
}

var (
	reAccepts = regexp.MustCompile(`Server accepts key: (\S+)`)
	reDenied  = regexp.MustCompile(`Permission denied \(([^)]*)\)`)
)

// CheckAuth opens a fresh connection (no multiplexing), offering public keys
// only and never prompting, and reports what happened.
func CheckAuth(alias string) (state, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-v", "-T", "-o", "BatchMode=yes", "-o", "ControlMaster=no",
		"-o", "ControlPath=none", "-o", "ConnectTimeout=5", "-o", "PreferredAuthentications=publickey", alias, "true")
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.Run()
	return classifyAuth(errb.String())
}

// classifyAuth reads ssh -v output.
func classifyAuth(err string) (state, key string) {
	switch {
	case strings.Contains(err, "Authenticated to"):
		if m := reAccepts.FindStringSubmatch(err); m != nil {
			key = filepath.Base(m[1])
		}
		return "key", key
	case strings.Contains(err, "HOST IDENTIFICATION HAS CHANGED"):
		return "hostkey", ""
	case strings.Contains(err, "Host key verification failed"):
		return "newkey", ""
	}
	if m := reDenied.FindStringSubmatch(err); m != nil {
		if strings.Contains(m[1], "password") || strings.Contains(m[1], "keyboard") {
			return "pw", ""
		}
		return "nokey", ""
	}
	return "down", ""
}

// Audit checks key login on the given hosts in parallel and updates the cache.
// A host that is merely offline keeps its last known answer.
func Audit(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	type res struct{ state, key string }
	rs := parallel(aliases, 16, func(a string) res { s, k := CheckAuth(a); return res{s, k} })
	m := LoadAuth()
	now := time.Now().Unix()
	for i, a := range aliases {
		if _, known := m[a]; rs[i].state == "down" && known {
			continue
		}
		m[a] = Auth{rs[i].state, rs[i].key, now}
	}
	saveAuth(m)
}

// ---------- usage history ----------

func historyPath() string { return config.State("history") }

// Uses counts past connections per alias.
func Uses() map[string]int {
	m := map[string]int{}
	b, _ := os.ReadFile(historyPath())
	for _, a := range strings.Fields(string(b)) {
		m[a]++
	}
	return m
}

// Remember records a connection.
func Remember(alias string) {
	f, err := os.OpenFile(historyPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, alias)
}

// Sorted returns all hosts, most used first, then by name.
func Sorted() []sshcfg.Host {
	hs := sshcfg.Parse()
	cnt := Uses()
	sort.SliceStable(hs, func(i, j int) bool {
		if cnt[hs[i].Alias] != cnt[hs[j].Alias] {
			return cnt[hs[i].Alias] > cnt[hs[j].Alias]
		}
		return strings.ToLower(hs[i].Alias) < strings.ToLower(hs[j].Alias)
	})
	return hs
}

// ---------- list filter ----------

// Filter narrows the list: one group and/or all | hide | only PROD.
type Filter struct{ Group, Prod string }

func filterPath() string { return config.State("filter") }

// LoadFilter reads the current filter.
func LoadFilter() Filter {
	f := Filter{Prod: "all"}
	b, _ := os.ReadFile(filterPath())
	for _, l := range strings.Split(string(b), "\n") {
		switch k, v, _ := strings.Cut(l, "="); k {
		case "group":
			f.Group = v
		case "prod":
			f.Prod = v
		}
	}
	return f
}

func (f Filter) save() {
	os.WriteFile(filterPath(), []byte("group="+f.Group+"\nprod="+f.Prod+"\n"), 0o600)
}

// Apply keeps the hosts that pass the filter.
func (f Filter) Apply(hs []sshcfg.Host) []sshcfg.Host {
	var out []sshcfg.Host
	for _, h := range hs {
		if f.Group != "" && !strings.EqualFold(h.Group, f.Group) {
			continue
		}
		if (f.Prod == "hide" && h.IsProd()) || (f.Prod == "only" && !h.IsProd()) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// StepFilter changes the filter: reset | next | prev (group) | prod (all → hide → only).
func StepFilter(op string) {
	f := LoadFilter()
	switch op {
	case "reset":
		f = Filter{Prod: "all"}
	case "next", "prev":
		groups := append([]string{""}, sshcfg.Groups()...)
		i := 0
		for k, g := range groups {
			if g == f.Group {
				i = k
			}
		}
		d := 1
		if op == "prev" {
			d = len(groups) - 1
		}
		f.Group = groups[(i+d)%len(groups)]
	case "prod":
		f.Prod = map[string]string{"all": "hide", "hide": "only"}[f.Prod]
		if f.Prod == "" {
			f.Prod = "all"
		}
	}
	f.save()
}

// Label is the title above the list: what is shown and how many hosts.
func Label() string {
	f := LoadFilter()
	var parts []string
	if f.Group != "" {
		parts = append(parts, f.Group)
	}
	switch f.Prod {
	case "hide":
		parts = append(parts, "without PROD")
	case "only":
		parts = append(parts, "PROD only")
	}
	what := strings.Join(parts, " · ")
	if what == "" {
		what = "all hosts"
	}
	return fmt.Sprintf(" %s · %d ", what, len(f.Apply(sshcfg.Parse())))
}

// ---------- refresh ----------

// Refresh probes every host and writes the status cache. Key login is re-checked
// on reachable hosts whose last check is older than AuthTTL (or on all of them with force).
func Refresh(force bool) {
	if force {
		ForgetResolved()
	}
	hs := Sorted()
	aliases := make([]string, len(hs))
	for i, h := range hs {
		aliases[i] = h.Alias
	}
	infos := ResolveAll(aliases)
	states := parallel(aliases, 64, func(a string) string { return Probe(infos[a]) })
	st := make(map[string]Status, len(aliases))
	for i, a := range aliases {
		st[a] = Status{infos[a].Addr(), states[i]}
	}
	saveStatus(aliases, st)

	known, now := LoadAuth(), time.Now().Unix()
	var todo []string
	for i, a := range aliases {
		if states[i] != "down" && (force || now-known[a].TS > int64(AuthTTL.Seconds())) {
			todo = append(todo, a)
		}
	}
	Audit(todo)
}
