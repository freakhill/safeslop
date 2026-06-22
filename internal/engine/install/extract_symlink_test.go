package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// tgzWithSymlink builds a .tar.gz with one regular file and one symlink entry (linkname is the raw
// symlink target). Mirrors lima's tree, whose share/doc/lima/templates -> ../../lima/templates points
// elsewhere within the tree but OUTSIDE its own parent dir.
func tgzWithSymlink(t *testing.T, regName, symName, linkname string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: regName, Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("ok\n"))
	if err := tw.WriteHeader(&tar.Header{Name: symName, Linkname: linkname, Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtractTarGzAllowsIntraTreeSymlink(t *testing.T) {
	// share/lima/templates (real) + share/doc/lima/templates -> ../../lima/templates (resolves within dest).
	art := tgzWithSymlink(t, "share/lima/templates/default.yaml", "share/doc/lima/templates", "../../lima/templates")
	dest := t.TempDir()
	if err := extractTarGz(art, dest); err != nil {
		t.Fatalf("intra-tree symlink must extract: %v", err)
	}
	link := filepath.Join(dest, "share/doc/lima/templates")
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink not created: %v (err %v)", fi.Mode(), err)
	}
}

func TestExtractTarGzRejectsEscapingSymlink(t *testing.T) {
	// A symlink whose resolved target escapes the extraction root must be refused.
	art := tgzWithSymlink(t, "bin/x", "bin/evil", "../../../../../../etc/passwd")
	if err := extractTarGz(art, t.TempDir()); err == nil {
		t.Fatal("a symlink escaping dest must be rejected")
	}
}
