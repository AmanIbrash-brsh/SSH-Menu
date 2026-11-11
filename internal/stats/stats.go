// Package stats collects read-only system information from a host for the
// preview panel and caches it for a few minutes.
package stats

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/config"
)

const (
	TTL        = 10 * time.Minute // system info is considered fresh this long
	NoShellTTL = 24 * time.Hour   // hosts without a shell (git, network gear) are retried rarely
	marker     = "__SM__"
)

//go:embed stats.sh
var script string

// Disk is one mounted filesystem, sizes in KiB.
type Disk struct {
	Mount string `json:"mount"`
	Used  int64  `json:"used"`
	Total int64  `json:"total"`
}

// Data is one snapshot of a host.
type Data struct {
	TS       int64    `json:"ts"`
	NoShell  bool     `json:"noshell,omitempty"` // login works, but there is no shell to ask
	OS       string   `json:"os,omitempty"`
	Up       int64    `json:"up,omitempty"` // seconds
	Load     string   `json:"load,omitempty"`
	CPU      int      `json:"cpu,omitempty"`
	MemUsed  int64    `json:"mem_used,omitempty"` // KiB
	MemTotal int64    `json:"mem_total,omitempty"`
	Disks    []Disk   `json:"disks,omitempty"`
	HasUnits bool     `json:"has_units,omitempty"` // systemd present
	Failed   []string `json:"failed,omitempty"`
	Reboot   bool     `json:"reboot,omitempty"`
	HasUpd   bool     `json:"has_upd,omitempty"`
	Upd      int      `json:"upd,omitempty"`
	UpdSec   int      `json:"upd_sec,omitempty"`
	HasDock  bool     `json:"has_docker,omitempty"`
	Docker   int      `json:"docker,omitempty"`
}

// Age is how old the snapshot is.
func (d *Data) Age() time.Duration { return time.Since(time.Unix(d.TS, 0)) }

// Fresh reports whether the snapshot can be shown without asking the host again.
func (d *Data) Fresh() bool {
	if d.NoShell {
		return d.Age() < NoShellTTL
	}
	return d.Age() < TTL
}

var reUnsafe = regexp.MustCompile(`[^\w.-]`)

func dir() string {
	d := config.State("stats")
	os.MkdirAll(d, 0o700)
	return d
}

func file(alias string) string {
	return filepath.Join(dir(), reUnsafe.ReplaceAllString(alias, "_")+".json")
}

// Load returns the cached snapshot, or nil.
func Load(alias string) *Data {
	b, err := os.ReadFile(file(alias))
	if err != nil {
		return nil
	}
	var d Data
	if json.Unmarshal(b, &d) != nil || d.TS == 0 {
		return nil
	}
	return &d
}

// Forget drops every cached snapshot (Ctrl+R).
func Forget() {
	m, _ := filepath.Glob(filepath.Join(dir(), "*.json"))
	for _, f := range m {
		os.Remove(f)
	}
}

func ssh(ctx context.Context, alias, stdin string, remote string) (string, int) {
	cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=4", alias, remote)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	code := 0
	if err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

// Fetch asks the host and updates the cache. It returns nil when the host could
// not be reached or logged into (nothing is cached then).
//
// A host we have never seen a shell on first gets a harmless one-word probe:
// network gear (FortiOS reads "sh" as "show") and git servers must never be fed the script.
func Fetch(alias string, knownShell bool) *Data {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, code := "", 0
	if !knownShell {
		out, code = ssh(ctx, alias, "", "echo "+marker)
	}
	if knownShell || strings.Contains(out, marker) {
		out, code = ssh(ctx, alias, script, "sh -s")
	}
	var d *Data
	if !strings.Contains(out, marker) {
		if code == 255 || code == -1 {
			return nil
		}
		d = &Data{TS: time.Now().Unix(), NoShell: true}
	} else {
		d = Parse(out)
	}
	if b, err := json.Marshal(d); err == nil {
		config.WriteAtomic(file(alias), b, 0o600)
	}
	return d
}

// Parse turns the script output into a snapshot.
func Parse(out string) *Data {
	d := &Data{TS: time.Now().Unix()}
	_, body, _ := strings.Cut(out, marker)
	for _, l := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch k {
		case "os":
			d.OS = v
		case "up":
			d.Up, _ = strconv.ParseInt(v, 10, 64)
		case "load":
			d.Load = v
		case "cpu":
			d.CPU, _ = strconv.Atoi(v)
		case "mem":
			if f := strings.Fields(v); len(f) == 2 {
				d.MemUsed, _ = strconv.ParseInt(f[0], 10, 64)
				d.MemTotal, _ = strconv.ParseInt(f[1], 10, 64)
			}
		case "df":
			if f := strings.SplitN(v, " ", 3); len(f) == 3 {
				u, _ := strconv.ParseInt(f[0], 10, 64)
				t, _ := strconv.ParseInt(f[1], 10, 64)
				d.Disks = append(d.Disks, Disk{Mount: f[2], Used: u, Total: t})
			}
		case "units":
			d.HasUnits = true
		case "fail":
			d.Failed = append(d.Failed, v)
		case "reboot":
			d.Reboot = true
		case "upd":
			if f := strings.Fields(v); len(f) >= 1 {
				d.HasUpd = true
				d.Upd, _ = strconv.Atoi(f[0])
				if len(f) > 1 {
					d.UpdSec, _ = strconv.Atoi(f[1])
				}
			}
		case "docker":
			d.HasDock = true
			d.Docker, _ = strconv.Atoi(v)
		}
	}
	return d
}
