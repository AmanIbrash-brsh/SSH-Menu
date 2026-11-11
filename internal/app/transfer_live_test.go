//go:build live

package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Real transfer against a host: go test -tags live -run Live ./internal/app -v
// SSHM_TEST_HOST must be a throw-away / non-production alias.
func TestLiveTransfer(t *testing.T) {
	host := os.Getenv("SSHM_TEST_HOST")
	if host == "" {
		t.Skip("SSHM_TEST_HOST not set")
	}
	src, back := t.TempDir(), t.TempDir()
	dir := filepath.Join(src, "sshm test dir")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	big := filepath.Join(dir, "big file.bin")
	exec.Command("sh", "-c", `head -c 40000000 /dev/urandom > "$1"`, "sh", big).Run()
	os.WriteFile(filepath.Join(dir, "sub", "note.txt"), []byte("привет"), 0o644)
	sum := func(p string) string { b, _ := os.ReadFile(p); return fmt.Sprintf("%x", sha256.Sum256(b)) }

	size := localSize([]string{dir})
	if f := runJobs([]string{host}, func(string) int64 { return size }, func(h string, j *job) error {
		return uploadTo(h, []string{dir}, "/tmp", true, j)
	}); f != 0 {
		t.Fatal("upload failed")
	}
	defer exec.Command("ssh", host, "rm -rf '/tmp/sshm test dir'").Run()

	if n := remoteSize(host, "/tmp", []string{"sshm test dir"}, true); n < size {
		t.Errorf("remoteSize = %d, sent %d", n, size)
	}
	if f := runJobs([]string{host}, func(h string) int64 { return remoteSize(h, "/tmp", []string{"sshm test dir"}, true) },
		func(h string, j *job) error { return downloadFrom(h, "/tmp", []string{"sshm test dir"}, back, true, j) }); f != 0 {
		t.Fatal("download failed")
	}
	got := filepath.Join(back, "sshm test dir")
	if sum(big) != sum(filepath.Join(got, "big file.bin")) {
		t.Error("big file differs after the round trip")
	}
	if b, _ := os.ReadFile(filepath.Join(got, "sub", "note.txt")); string(b) != "привет" {
		t.Errorf("note.txt = %q", b)
	}
	// errors from the far side are reported, not swallowed
	if f := runJobs([]string{host}, nil, func(h string, j *job) error {
		return uploadTo(h, []string{dir}, "/nonexistent-folder-xyz", true, j)
	}); f != 1 {
		t.Error("upload into a missing folder must fail")
	}
}
