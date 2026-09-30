package attach

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(t *testing.T) *Files {
	t.Helper()
	files, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestAnImageIsKeptWithItsDimensions(t *testing.T) {
	files := open(t)
	kept, err := files.Keep(bytes.NewReader(pngBytes(t, 64, 32)), "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Kind != store.AttachmentImage || kept.MIME != "image/png" || kept.Width != 64 || kept.Height != 32 {
		t.Errorf("kept %+v, want a 64×32 png image", kept)
	}
	if _, err := os.Stat(files.Path(kept.Path)); err != nil {
		t.Errorf("kept file missing: %v", err)
	}
}

func TestAnyOtherFileIsAFile(t *testing.T) {
	kept, err := open(t).Keep(strings.NewReader("plain words\n"), "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Kind != store.AttachmentFile || !strings.HasPrefix(kept.MIME, "text/plain") || kept.Size != 12 {
		t.Errorf("kept %+v, want a 12-byte text file", kept)
	}
}

func TestOverTheLimitKeepsNothing(t *testing.T) {
	files := open(t)
	_, err := files.Keep(io.LimitReader(zeros{}, MaxSize+1), "big.bin")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	left, _ := os.ReadDir(files.dir)
	if len(left) != 0 {
		t.Errorf("left %d files behind", len(left))
	}
	// exactly at the limit is fine
	if _, err := files.Keep(io.LimitReader(zeros{}, MaxSize), "edge.bin"); err != nil {
		t.Errorf("a file of exactly MaxSize: %v", err)
	}
}

func TestTheSameFileSentTwiceIsKeptTwice(t *testing.T) {
	// Each message owns its copy: expiring one must not remove the other's.
	files := open(t)
	first, _ := files.Keep(strings.NewReader("same"), "a.txt")
	second, _ := files.Keep(strings.NewReader("same"), "a.txt")
	if first.Path == second.Path || first.SHA256 != second.SHA256 {
		t.Fatalf("paths %q and %q, want two copies of one content", first.Path, second.Path)
	}
	if err := files.Remove(first.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(files.Path(second.Path)); err != nil {
		t.Errorf("removing one copy removed the other: %v", err)
	}
	if err := files.Remove(first.Path); err != nil {
		t.Errorf("removing an already-removed file: %v", err)
	}
}

func TestRemoveStaysInsideTheDirectory(t *testing.T) {
	for _, rel := range []string{"", "../spool.db", "sub/x", "/etc/passwd"} {
		if err := open(t).Remove(rel); err == nil {
			t.Errorf("Remove(%q) = nil, want a refusal", rel)
		}
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"shot.png":                       "shot.png",
		"../../etc/passwd":               "passwd",
		`C:\Users\me\Screen Shot 1.png`:  "Screen_Shot_1.png",
		".bashrc":                        "bashrc",
		"":                               "file",
		"résumé.pdf":                     "r_sum_.pdf",
		"$(rm -rf ~).sh":                 "__rm_-rf___.sh",
		strings.Repeat("a", 100) + ".md": strings.Repeat("a", 77) + ".md",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
