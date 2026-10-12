package clicore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clientFileRoot() *Command {
	root := &Command{Name: "t", Children: []*Command{{
		Name: "push", SideEffect: EffectNone, Authority: &Authority{},
		Flags: []Flag{{Name: "file", Kind: KindString, ClientFile: &ClientFile{Extensions: []string{".md"}, MaxBytes: 64}}, {Name: "note", Kind: KindString}},
		Run:   func(context.Context, *Invocation) (interface{}, error) { return nil, nil },
	}}}
	Prepare(root)
	return root
}

func TestClientFilesAreReadOnlyWithinTheirLimits(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "de.md")
	if err := os.WriteFile(good, []byte("# đề"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "id_ed25519")
	_ = os.WriteFile(secret, []byte("PRIVATE"), 0o600)
	big := filepath.Join(dir, "big.md")
	_ = os.WriteFile(big, []byte(strings.Repeat("x", 65)), 0o600)
	binary := filepath.Join(dir, "bin.md")
	_ = os.WriteFile(binary, []byte{0xff, 0xfe, 0x00}, 0o600)
	root := clientFileRoot()

	invocation, perr := Parse(root, []string{"push", "--file", good, "--note", good})
	if perr != nil {
		t.Fatal(perr)
	}
	if err := ExpandClientFiles(invocation); err != nil || invocation.Raw["file"][0] != "# đề" || invocation.Raw["note"][0] != good {
		t.Fatalf("expand: %v %v", invocation.Raw, err)
	}
	for path, code := range map[string]string{secret: "LOCAL_FILE_TYPE", big: "LOCAL_FILE_TOO_LARGE", binary: "LOCAL_FILE_NOT_TEXT", dir + "/none.md": "LOCAL_FILE_UNREADABLE", dir: "LOCAL_FILE_TYPE"} {
		invocation, _ := Parse(root, []string{"push", "--file", path})
		if err := ExpandClientFiles(invocation); err == nil || err.Code != code {
			t.Errorf("%s: %v, want %s", path, err, code)
		}
	}
	args, err := ExpandClientFileArgs(root, []string{"push", "--file=" + good, "--note", "x"})
	if err != nil || args[1] != "--file=# đề" || args[3] != "x" {
		t.Fatalf("args: %q %v", args, err)
	}
	args, err = ExpandClientFileArgs(root, []string{"push", "--file", good})
	if err != nil || args[2] != "# đề" {
		t.Fatalf("args: %q %v", args, err)
	}
}

func TestRunNeverExpandsAClientFile(t *testing.T) {
	// The authority runtime executes through Run: a path that reaches it stays text.
	dir := t.TempDir()
	path := filepath.Join(dir, "de.md")
	_ = os.WriteFile(path, []byte("content"), 0o600)
	var seen string
	root := clientFileRoot()
	root.Children[0].Run = func(_ context.Context, inv *Invocation) (interface{}, error) {
		seen = inv.String("file")
		return nil, nil
	}
	Run(context.Background(), root, []string{"push", "--file", path}, Streams{})
	if seen != path {
		t.Fatalf("Run expanded the file: %q", seen)
	}
}

func TestABundleCarriesOnlyImagesBesideTheFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "de", "hinh"), 0o700)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	_ = os.WriteFile(filepath.Join(dir, "de", "hinh", "a.png"), png, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "secret.png"), png, 0o600)
	_ = os.Symlink(filepath.Join(dir, "secret.png"), filepath.Join(dir, "de", "hinh", "link.png"))
	flag := Flag{Name: "file", ClientFile: &ClientFile{Extensions: []string{".md"}, MaxBytes: 1 << 16,
		Bundle: &ClientBundle{Flag: "image", Extensions: []string{".png"}, MaxBytes: 1 << 16, MaxCount: 2}}}
	md := filepath.Join(dir, "de", "de.md")

	images, err := ReadClientBundle(flag, md, "![a](hinh/a.png) ![b](https://x/y.png) ![c](asset:123) ![a again](hinh/a.png)")
	if err != nil || len(images) != 1 || images[0].Path != "hinh/a.png" {
		t.Fatalf("images = %+v %v", images, err)
	}
	for text, code := range map[string]string{
		"![x](../secret.png)":          "LOCAL_FILE_OUTSIDE",
		"![x](" + dir + "/secret.png)": "LOCAL_FILE_OUTSIDE",
		"![x](hinh/link.png)":          "LOCAL_FILE_UNREADABLE",
		"![x](hinh/none.png)":          "LOCAL_FILE_UNREADABLE",
		"![x](de.md)":                  "LOCAL_FILE_TYPE",
		"![1](hinh/a.png)![2](./hinh/a.png)![3](hinh//a.png)": "LOCAL_FILE_TOO_MANY",
	} {
		if _, err := ReadClientBundle(flag, md, text); err == nil || err.Code != code {
			t.Errorf("%s: %v, want %s", text, err, code)
		}
	}
}
