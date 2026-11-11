package hosts

import (
	"testing"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/sshcfg"
)

func TestClassifyAuth(t *testing.T) {
	cases := []struct{ in, state, key string }{
		{"debug1: Server accepts key: /home/u/.ssh/id_ed25519 ED25519 SHA256:x\nAuthenticated to 10.0.0.1 ([10.0.0.1]:22) using \"publickey\".", "key", "id_ed25519"},
		{"Authenticated to h using \"publickey\".", "key", ""},
		{"u@h: Permission denied (publickey,password).", "pw", ""},
		{"u@h: Permission denied (publickey,keyboard-interactive).", "pw", ""},
		{"u@h: Permission denied (publickey).", "nokey", ""},
		{"@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @\nHost key verification failed.", "hostkey", ""},
		{"No ED25519 host key is known for h and you have requested strict checking.\nHost key verification failed.", "newkey", ""},
		{"ssh: connect to host h port 22: No route to host", "down", ""},
		{"", "down", ""},
	}
	for _, c := range cases {
		if s, k := classifyAuth(c.in); s != c.state || k != c.key {
			t.Errorf("classifyAuth(%.40q) = %s,%q; want %s,%q", c.in, s, k, c.state, c.key)
		}
	}
}

func TestFilter(t *testing.T) {
	hs := []sshcfg.Host{
		{Alias: "a", Group: "Proxmox", Env: "prod"},
		{Alias: "b", Group: "proxmox"},
		{Alias: "c", Group: "Dev", Env: "PROD"},
		{Alias: "d"},
	}
	names := func(f Filter) string {
		s := ""
		for _, h := range f.Apply(hs) {
			s += h.Alias
		}
		return s
	}
	for _, c := range []struct {
		f    Filter
		want string
	}{
		{Filter{Prod: "all"}, "abcd"},
		{Filter{Group: "Proxmox", Prod: "all"}, "ab"},
		{Filter{Prod: "hide"}, "bd"},
		{Filter{Prod: "only"}, "ac"},
		{Filter{Group: "proxmox", Prod: "only"}, "a"},
		{Filter{Group: "none", Prod: "all"}, ""},
	} {
		if got := names(c.f); got != c.want {
			t.Errorf("%+v -> %q, want %q", c.f, got, c.want)
		}
	}
}

func TestAddr(t *testing.T) {
	if got := (Info{User: "root", HostName: "h", Port: "2222"}).Addr(); got != "root@h:2222" {
		t.Error(got)
	}
}
