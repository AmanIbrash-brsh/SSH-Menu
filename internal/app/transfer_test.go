package app

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// what uploadTo writes must be what extract (and a real tar) can read back
func TestTarRoundTrip(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(src, "dir", "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "dir", "a.txt"), []byte("hello"), 0o640)
	os.WriteFile(filepath.Join(src, "dir", "sub", "b b.bin"), bytes.Repeat([]byte{7}, 100000), 0o755)
	os.Symlink("a.txt", filepath.Join(src, "dir", "link"))
	os.WriteFile(filepath.Join(src, "single.txt"), []byte("x"), 0o644)

	paths := []string{filepath.Join(src, "dir"), filepath.Join(src, "single.txt")}
	if n := localSize(paths); n != 5+100000+1 {
		t.Fatalf("localSize = %d", n)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	up := &job{}
	for _, p := range paths {
		if err := addToTar(tw, p, up); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	if up.done.Load() != 100006 {
		t.Errorf("upload counted %d bytes", up.done.Load())
	}
	down := &job{}
	if err := extract(&buf, dst, down); err != nil {
		t.Fatal(err)
	}
	if down.done.Load() != 100006 {
		t.Errorf("download counted %d bytes", down.done.Load())
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "dir", "a.txt")); string(b) != "hello" {
		t.Errorf("a.txt = %q", b)
	}
	if st, err := os.Stat(filepath.Join(dst, "dir", "sub", "b b.bin")); err != nil || st.Size() != 100000 || st.Mode().Perm() != 0o755 {
		t.Errorf("b b.bin: %v %v", st, err)
	}
	if l, _ := os.Readlink(filepath.Join(dst, "dir", "link")); l != "a.txt" {
		t.Errorf("link -> %q", l)
	}
	if _, err := os.Stat(filepath.Join(dst, "single.txt")); err != nil {
		t.Error(err)
	}
}

// a hostile archive must not be able to write outside the download folder
func TestExtractRefusesEscapes(t *testing.T) {
	mk := func(entries ...*tar.Header) *bytes.Buffer {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range entries {
			if h.Typeflag == tar.TypeReg {
				h.Size = 1
			}
			tw.WriteHeader(h)
			if h.Typeflag == tar.TypeReg {
				tw.Write([]byte("x"))
			}
		}
		tw.Close()
		return &buf
	}
	for name, archive := range map[string]*bytes.Buffer{
		"dotdot":   mk(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}),
		"nested":   mk(&tar.Header{Name: "a/../../evil", Typeflag: tar.TypeReg, Mode: 0o644}),
		"absolute": mk(&tar.Header{Name: "/tmp/evil", Typeflag: tar.TypeReg, Mode: 0o644}),
		"via link": mk(&tar.Header{Name: "out", Typeflag: tar.TypeSymlink, Linkname: ".."},
			&tar.Header{Name: "out/evil", Typeflag: tar.TypeReg, Mode: 0o644}),
	} {
		root := t.TempDir()
		dst := filepath.Join(root, "dl")
		os.Mkdir(dst, 0o755)
		if err := extract(archive, dst, &job{}); err == nil {
			t.Errorf("%s: extract accepted an escaping path", name)
		}
		if _, err := os.Stat(filepath.Join(root, "evil")); err == nil {
			t.Errorf("%s: a file was written outside the destination", name)
		}
	}
}

func TestRemoteExpr(t *testing.T) {
	for in, want := range map[string]string{
		"":          `"$HOME"`,
		"~":         `"$HOME"`,
		"~/my dir":  `"$HOME"/'my dir'`,
		"/var/log":  `'/var/log'`,
		"it's here": `'it'\''s here'`,
	} {
		if got := remoteExpr(in); got != want {
			t.Errorf("remoteExpr(%q) = %s, want %s", in, got, want)
		}
	}
}
