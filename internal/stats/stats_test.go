package stats

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	out := `Welcome banner
__SM__
os=Ubuntu 22.04.5 LTS
up=61023
load=0.10 0.20 0.30
cpu=4
mem=939368 6159672
df=14881032 59740388 /
df=100 200 /mnt/my disk
units=1
fail=fwupd-refresh.service
fail=motd-news.service
reboot=1
upd=69 3
docker=2
`
	d := Parse(out)
	if d.OS != "Ubuntu 22.04.5 LTS" || d.Up != 61023 || d.Load != "0.10 0.20 0.30" || d.CPU != 4 {
		t.Errorf("basics: %+v", d)
	}
	if d.MemUsed != 939368 || d.MemTotal != 6159672 {
		t.Errorf("mem: %+v", d)
	}
	want := []Disk{{"/", 14881032, 59740388}, {"/mnt/my disk", 100, 200}}
	if !reflect.DeepEqual(d.Disks, want) {
		t.Errorf("disks: %+v (a mount point may contain spaces)", d.Disks)
	}
	if !d.HasUnits || len(d.Failed) != 2 || !d.Reboot || !d.HasUpd || d.Upd != 69 || d.UpdSec != 3 || !d.HasDock || d.Docker != 2 {
		t.Errorf("rest: %+v", d)
	}
	if d.NoShell || !d.Fresh() {
		t.Error("a new snapshot is fresh and has a shell")
	}
	// a minimal host (no systemd, no package manager, no docker)
	m := Parse("__SM__\nup=5\nload=0 0 0\n")
	if m.HasUnits || m.HasUpd || m.HasDock || m.Reboot {
		t.Errorf("absent tools must stay absent: %+v", m)
	}
}
