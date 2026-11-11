package app

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/fz"
	"github.com/AmanIbrash-brsh/SSH-Menu/internal/ui"
)

// Files travel as a tar stream over plain ssh (`tar` on the far side), so the
// menu knows exactly how many bytes went through and can draw a progress bar
// for every host, also when several hosts are served in parallel.

// ---------- progress ----------

type job struct {
	name  string
	total int64 // 0 = unknown
	done  atomic.Int64
	start time.Time

	mu   sync.Mutex
	end  time.Time
	err  error
	over bool
}

func (j *job) finish(err error) {
	j.mu.Lock()
	j.err, j.over, j.end = err, true, time.Now()
	j.mu.Unlock()
}

func humanBytes(n int64) string {
	f := float64(n)
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", f/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", f/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", f/(1<<30))
}

func (j *job) line(width int) string {
	j.mu.Lock()
	over, err, end := j.over, j.err, j.end
	j.mu.Unlock()
	name := j.name + strings.Repeat(" ", max(0, width-len(j.name)))
	done := j.done.Load()
	if over && err != nil {
		return failLine(name) + "  " + ui.Dim + err.Error() + ui.Reset
	}
	if over {
		secs := end.Sub(j.start).Seconds()
		speed := ""
		if secs > 0.2 {
			speed = fmt.Sprintf(" · %s/s", humanBytes(int64(float64(done)/secs)))
		}
		return okLine(name) + fmt.Sprintf("  %s%s in %.1fs%s%s", ui.Dim, humanBytes(done), secs, speed, ui.Reset)
	}
	secs := time.Since(j.start).Seconds()
	speed := ""
	if secs > 0.5 && done > 0 {
		speed = fmt.Sprintf("  %s/s", humanBytes(int64(float64(done)/secs)))
	}
	if j.total <= 0 {
		return "  " + name + fmt.Sprintf("  %s%s%s", ui.Dim, humanBytes(done)+speed, ui.Reset)
	}
	pct := min(100, float64(done)*100/float64(j.total))
	const w = 24
	n := int(pct / 100 * w)
	bar := cHi + strings.Repeat("━", n) + "\x1b[38;2;70;58;62m" + strings.Repeat("━", w-n) + ui.Reset
	return "  " + name + fmt.Sprintf("  %s %3.0f%%  %s%s / %s%s%s", bar, pct, ui.Dim, humanBytes(done), humanBytes(j.total), speed, ui.Reset)
}

// watch redraws one line per job until all are finished.
func watch(jobs []*job) {
	width := 0
	for _, j := range jobs {
		width = max(width, len(j.name))
	}
	draw := func(first bool) {
		var b strings.Builder
		if !first {
			fmt.Fprintf(&b, "\x1b[%dA", len(jobs))
		}
		for _, j := range jobs {
			b.WriteString("\r\x1b[2K" + j.line(width) + "\n")
		}
		os.Stdout.WriteString(b.String())
	}
	fmt.Print("\x1b[?25l") // hide the cursor while bars move
	defer fmt.Print("\x1b[?25h")
	draw(true)
	for {
		time.Sleep(120 * time.Millisecond)
		all := true
		for _, j := range jobs {
			j.mu.Lock()
			all = all && j.over
			j.mu.Unlock()
		}
		draw(false)
		if all {
			return
		}
	}
}

// runJobs runs work for every host in parallel while drawing progress; it returns the number of failures.
func runJobs(names []string, total func(name string) int64, work func(name string, j *job) error) int {
	jobs := make([]*job, len(names))
	for i, n := range names {
		jobs[i] = &job{name: n, start: time.Now()}
	}
	for i, n := range names {
		go func() {
			if total != nil {
				jobs[i].total = total(n)
			}
			jobs[i].start = time.Now()
			jobs[i].finish(work(n, jobs[i]))
		}()
	}
	watch(jobs)
	failed := 0
	for _, j := range jobs {
		if j.err != nil {
			failed++
		}
	}
	return failed
}

type counter struct{ n *atomic.Int64 }

func (c counter) Write(p []byte) (int, error) { c.n.Add(int64(len(p))); return len(p), nil }

// ---------- remote paths ----------

// remoteExpr turns a path typed by the user into a /bin/sh word on the far side:
// "~" and "~/x" are relative to the remote home, everything else is literal.
func remoteExpr(p string) string {
	switch {
	case p == "" || p == "~" || p == "~/":
		return `"$HOME"`
	case strings.HasPrefix(p, "~/"):
		return `"$HOME"/` + fz.Quote(p[2:])
	}
	return fz.Quote(p)
}

func sshBase(batch bool) []string {
	a := []string{"-T"}
	if batch {
		a = append(a, "-o", "BatchMode=yes", "-o", "ConnectTimeout=8")
	}
	return a
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func remoteErr(err error, stderr string) error {
	if err == nil {
		return nil
	}
	if msg := lastLine(stderr); msg != "" {
		return errors.New(msg)
	}
	return err
}

// ---------- upload ----------

// localSize is the number of content bytes under the given paths.
func localSize(paths []string) int64 {
	var n int64
	for _, p := range paths {
		filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
			if err == nil && info.Mode().IsRegular() {
				n += info.Size()
			}
			return nil
		})
	}
	return n
}

func addToTar(tw *tar.Writer, root string, j *job) error {
	base := filepath.Base(root)
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		name := base
		if rel != "." {
			name = filepath.Join(base, rel)
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(p)
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return nil // sockets and the like: skip
		}
		hdr.Name = filepath.ToSlash(name)
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", "" // files belong to whoever receives them
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(io.MultiWriter(tw, counter{&j.done}), f)
		return err
	})
}

// uploadTo sends local files and folders into a remote folder.
func uploadTo(host string, files []string, dest string, batch bool, j *job) error {
	remote := "cd -- " + remoteExpr(dest) + " && tar -xof -"
	cmd := exec.Command("ssh", append(sshBase(batch), host, remote)...)
	var errb strings.Builder
	cmd.Stderr = &errb
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tw := tar.NewWriter(in)
	var werr error
	for _, f := range files {
		if werr = addToTar(tw, f, j); werr != nil {
			break
		}
	}
	if werr == nil {
		werr = tw.Close()
	}
	in.Close()
	if err := remoteErr(cmd.Wait(), errb.String()); err != nil {
		return err // the far side explains failures better than a broken pipe does
	}
	return werr
}

// ---------- download ----------

// remoteSize asks the far side how big the selection is (0 when it cannot tell).
func remoteSize(host, parent string, names []string, batch bool) int64 {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fz.Quote(n)
	}
	remote := "cd -- " + remoteExpr(parent) + " && du -sbc -- " + strings.Join(q, " ") + " 2>/dev/null | tail -1 | cut -f1"
	out, err := exec.Command("ssh", append(sshBase(batch), host, remote)...).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return n
}

// safeTarget maps an archive member name to a path inside dest. Names that try
// to leave dest ("..", absolute paths, or a path through a symlink) are refused.
func safeTarget(dest, name string) (string, error) {
	clean := path.Clean("/" + name)[1:]
	if clean == "" || strings.HasPrefix(name, "/") || strings.Contains("/"+name+"/", "/../") {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	parts := strings.Split(clean, "/")
	cur := dest
	for _, p := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, p)
		if st, err := os.Lstat(cur); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("unsafe path in archive (through a link): %q", name)
		}
	}
	return filepath.Join(dest, filepath.FromSlash(clean)), nil
}

func extract(r io.Reader, dest string, j *job) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeTarget(dest, hdr.Name)
		if err != nil {
			return err
		}
		mode := os.FileMode(hdr.Mode) & 0o777
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o755)
			if st, err := os.Lstat(target); err == nil && !st.Mode().IsRegular() {
				os.Remove(target) // never write through an existing link
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(io.MultiWriter(f, counter{&j.done}), tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			os.Chtimes(target, hdr.ModTime, hdr.ModTime)
		case tar.TypeSymlink:
			os.MkdirAll(filepath.Dir(target), 0o755)
			os.Remove(target)
			os.Symlink(hdr.Linkname, target)
		}
	}
}

// downloadFrom fetches names (files or folders inside the remote folder parent) into dest.
func downloadFrom(host, parent string, names []string, dest string, batch bool, j *job) error {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fz.Quote(n)
	}
	remote := "tar -cf - -C " + remoteExpr(parent) + " -- " + strings.Join(q, " ")
	cmd := exec.Command("ssh", append(sshBase(batch), host, remote)...)
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	xerr := extract(out, dest, j)
	io.Copy(io.Discard, out)
	if err := remoteErr(cmd.Wait(), errb.String()); err != nil {
		return err
	}
	return xerr
}
