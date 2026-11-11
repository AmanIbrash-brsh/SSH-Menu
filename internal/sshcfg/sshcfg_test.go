package sshcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# my config

# group: Proxmox
# env: prod
# url: https://10.0.0.11:8006
Host pve1
    HostName 10.0.0.11
    User root

# group: Dev
Host web1 web2
    HostName 10.0.0.20

# a stray comment, not metadata

Host bare
    HostName 10.0.0.30

Host *.lab !skip
    User lab


# ── Defaults ──
# keep last
Host *
    ServerAliveInterval 30
`

func setup(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	Path = filepath.Join(dir, "config")
	BackupDir = func() string { return filepath.Join(dir, "backups") }
	if err := os.WriteFile(Path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParse(t *testing.T) {
	setup(t, sample)
	hs := Parse()
	var names []string
	for _, h := range hs {
		names = append(names, h.Alias)
	}
	if got := strings.Join(names, " "); got != "pve1 web1 web2 bare" {
		t.Fatalf("aliases = %q (wildcard hosts must be skipped)", got)
	}
	p := Find("pve1")
	if p.Group != "Proxmox" || !p.IsProd() || p.URL != "https://10.0.0.11:8006" || p.Line != 6 {
		t.Errorf("pve1 = %+v", *p)
	}
	if w := Find("web2"); w.Group != "Dev" || w.IsProd() {
		t.Errorf("web2 = %+v (both aliases of one Host line share its metadata)", *w)
	}
	if b := Find("bare"); b.Group != "" || b.Env != "" {
		t.Errorf("bare = %+v (metadata must not leak across a blank line)", *b)
	}
	if got := strings.Join(Groups(), ","); got != "Dev,Proxmox" {
		t.Errorf("groups = %q", got)
	}
}

func TestSetMeta(t *testing.T) {
	setup(t, sample)
	if err := SetMeta("bare", "group", "Lab"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t), "# group: Lab\nHost bare\n") {
		t.Errorf("group not added right above the host:\n%s", read(t))
	}
	if err := SetMeta("pve1", "group", "Cluster"); err != nil {
		t.Fatal(err)
	}
	if p := Find("pve1"); p.Group != "Cluster" || !p.IsProd() || p.URL == "" {
		t.Errorf("replacing one key must keep the others: %+v", *p)
	}
	if strings.Count(read(t), "# group: Cluster") != 1 || strings.Contains(read(t), "# group: Proxmox") {
		t.Errorf("old value must be replaced, not duplicated:\n%s", read(t))
	}
	ToggleProd("pve1")
	if Find("pve1").IsProd() {
		t.Error("ToggleProd should clear prod")
	}
	ToggleProd("pve1")
	if !Find("pve1").IsProd() {
		t.Error("ToggleProd should set prod")
	}
	if SetMeta("nope", "group", "x") == nil {
		t.Error("unknown host must be an error")
	}
	if m, _ := filepath.Glob(filepath.Join(BackupDir(), "config-*")); len(m) == 0 {
		t.Error("no backup was written")
	}
	// everything else in the file is untouched
	if !strings.Contains(read(t), "Host *.lab !skip\n    User lab\n") || !strings.HasSuffix(read(t), "    ServerAliveInterval 30\n") {
		t.Errorf("unrelated lines changed:\n%s", read(t))
	}
}

func TestAddGoesBeforeDefaults(t *testing.T) {
	setup(t, sample)
	err := Add(NewHost{Alias: "new1", HostName: "10.0.0.40", User: "root", Port: "2222", Group: "Dev", Env: "prod", Identity: "~/.ssh/k"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Host *.lab !skip\n    User lab\n\n# group: Dev\n# env: prod\nHost new1\n    HostName 10.0.0.40\n    User root\n    Port 2222\n    IdentityFile ~/.ssh/k\n    IdentitiesOnly yes\n\n\n# ── Defaults ──\n# keep last\nHost *\n"
	if !strings.Contains(read(t), want) {
		t.Errorf("new host must sit before the Host * block and its comments:\n%s", read(t))
	}
	if h := Find("new1"); h == nil || h.Group != "Dev" || !h.IsProd() {
		t.Errorf("new1 = %+v", h)
	}
	for _, bad := range []string{"new1", "has space", "wild*", ""} {
		if Add(NewHost{Alias: bad}) == nil {
			t.Errorf("Add(%q) must fail", bad)
		}
	}
}

func TestAddWithoutDefaults(t *testing.T) {
	setup(t, "Host a\n    HostName 1.1.1.1\n\n\n")
	if err := Add(NewHost{Alias: "b", Port: "22"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t); got != "Host a\n    HostName 1.1.1.1\n\nHost b\n    HostName b\n" {
		t.Errorf("got:\n%q", got)
	}
	setup(t, "")
	os.Remove(Path) // no config at all yet
	if err := Add(NewHost{Alias: "first", HostName: "h"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t); got != "Host first\n    HostName h\n" {
		t.Errorf("got:\n%q", got)
	}
}

func TestInclude(t *testing.T) {
	dir := setup(t, "Include conf.d/*.conf\n\n# group: Main\nHost main\n    HostName m\n")
	os.MkdirAll(filepath.Join(dir, "conf.d"), 0o700)
	inc := filepath.Join(dir, "conf.d", "team.conf")
	os.WriteFile(inc, []byte("# group: Team\n# env: prod\nHost shared\n    HostName s\n\nHost main\n    HostName other\n"), 0o600)

	hs := Parse()
	if len(hs) != 2 {
		t.Fatalf("want main + shared, got %+v", hs)
	}
	s := Find("shared")
	if s == nil || s.Group != "Team" || !s.IsProd() || s.File != inc || s.Line != 3 {
		t.Fatalf("shared = %+v", s)
	}
	if Find("main").File != Path {
		t.Error("the first definition of an alias wins")
	}
	if len(Files()) != 2 {
		t.Errorf("Files() = %v", Files())
	}
	// metadata of an included host is edited in the file that defines it
	if err := SetMeta("shared", "group", "Ops"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(inc)
	if !strings.Contains(string(b), "# group: Ops\nHost shared") || strings.Contains(read(t), "Ops") {
		t.Errorf("edit went to the wrong file:\n%s", b)
	}
}
