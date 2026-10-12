package clicore

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ClientFile is the contract of a flag whose typed value is a path on the caller's machine (see
// Flag.ClientFile). Only the client expands it: internal/dispatch calls ExpandClientFiles before an
// invocation travels to the authority runtime, and ExpandClientFileArgs before a LAB direct run. The
// runtime executes commands through Run, which never expands anything, so a path that reaches it is
// only ever treated as text — the server cannot be made to read its own files.
//
// The limits keep an agent driving the CLI through the tool protocol from turning the flag into a way
// to ship arbitrary local files to the platform: one of the listed extensions, a regular file, at most
// MaxBytes, valid UTF-8 text.
type ClientFile struct {
	// Extensions are the accepted suffixes, lower case with the dot (".md").
	Extensions []string `json:"extensions"`
	// MaxBytes is the largest file accepted.
	MaxBytes int64 `json:"maxBytes"`
	// Bundle, when set, also sends the images the (Markdown) file names: "![...](relative/path)".
	Bundle *ClientBundle `json:"bundle,omitempty"`
}

// ClientBundle is the images a ClientFile carries along, each sent as one value of Flag:
// {"path": "<as written in the file>", "data": "<base64>"}. Only paths inside the file's own folder are
// read — never an absolute path, never one that climbs out with "..".
type ClientBundle struct {
	Flag       string   `json:"flag"`
	Extensions []string `json:"extensions"`
	MaxBytes   int64    `json:"maxBytes"`
	MaxCount   int      `json:"maxCount"`
}

// BundledImage is one image of a bundle.
type BundledImage struct {
	Path string `json:"path"`
	Data string `json:"data"`
}

var imageReference = regexp.MustCompile(`!\[[^\]]*\]\(\s*([^)\s]+)\s*\)`)

// ReadClientBundle reads the images a Markdown file at path names, under the bundle's limits. Targets that
// are links or already-stored assets ("asset:<id>") are not files and are skipped; the server judges them.
func ReadClientBundle(flag Flag, path, content string) ([]BundledImage, *Error) {
	bundle := flag.ClientFile.Bundle
	if bundle == nil {
		return nil, nil
	}
	refuse := func(code, format string, args ...interface{}) *Error {
		return UsageError(code, format, args...).WithContextValue("flag", flag.Name)
	}
	base := filepath.Dir(filepath.Clean(strings.TrimSpace(path)))
	seen := map[string]bool{}
	var out []BundledImage
	for _, m := range imageReference.FindAllStringSubmatch(content, -1) {
		target := m[1]
		lower := strings.ToLower(target)
		if seen[target] || strings.HasPrefix(lower, "asset:") || strings.Contains(lower, "://") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(target, "//") {
			continue
		}
		seen[target] = true
		clean := filepath.Clean(filepath.FromSlash(target))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, refuse("LOCAL_FILE_OUTSIDE", "the image %s is outside the file's folder: keep images beside the file", target)
		}
		if len(out) == bundle.MaxCount {
			return nil, refuse("LOCAL_FILE_TOO_MANY", "the file names more than %d images", bundle.MaxCount)
		}
		data, err := readLimited(Flag{Name: flag.Name, ClientFile: &ClientFile{Extensions: bundle.Extensions, MaxBytes: bundle.MaxBytes}}, filepath.Join(base, clean), false)
		if err != nil {
			return nil, err.WithContextValue("image", target)
		}
		out = append(out, BundledImage{Path: target, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return out, nil
}

func bundleValues(images []BundledImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		body, _ := json.Marshal(image)
		out = append(out, string(body))
	}
	return out
}

// ExpandClientFiles replaces, in place, the value of every ClientFile flag of the invocation by the
// content of the file it names.
func ExpandClientFiles(invocation *Invocation) *Error {
	if invocation == nil || invocation.Command == nil {
		return nil
	}
	for _, flag := range invocation.Command.Flags {
		if flag.ClientFile == nil {
			continue
		}
		values := invocation.Raw[flag.Name]
		for i, path := range values {
			content, err := readClientFile(flag, path)
			if err != nil {
				return err
			}
			images, err := ReadClientBundle(flag, path, content)
			if err != nil {
				return err
			}
			values[i] = content
			if flag.ClientFile.Bundle != nil {
				invocation.Raw[flag.ClientFile.Bundle.Flag] = append(invocation.Raw[flag.ClientFile.Bundle.Flag], bundleValues(images)...)
			}
		}
	}
	return nil
}

// ExpandClientFileArgs returns args with the value of every ClientFile flag replaced by the file's
// content, for the paths that re-run a command line (LAB direct mode). Args that do not parse are
// returned unchanged: the run that follows reports the parse error itself.
func ExpandClientFileArgs(root *Command, args []string) ([]string, *Error) {
	invocation, err := Parse(root, args)
	if err != nil || invocation == nil || invocation.Command == nil {
		return args, nil
	}
	files := map[string]ClientFile{}
	for _, flag := range invocation.Command.Flags {
		if flag.ClientFile != nil {
			files[flag.Name] = *flag.ClientFile
		}
	}
	if len(files) == 0 {
		return args, nil
	}
	out := append([]string(nil), args...)
	var extra []string
	for i := 0; i < len(out); i++ {
		token := out[i]
		if !strings.HasPrefix(token, "--") {
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(token, "--"), "=")
		spec, ok := files[name]
		if !ok {
			continue
		}
		flag := Flag{Name: name, ClientFile: &spec}
		path, at := value, i
		if !inline {
			if i+1 >= len(out) {
				continue
			}
			path, at = out[i+1], i+1
			i++
		}
		content, readErr := readClientFile(flag, path)
		if readErr != nil {
			return nil, readErr
		}
		images, readErr := ReadClientBundle(flag, path, content)
		if readErr != nil {
			return nil, readErr
		}
		if inline {
			out[at] = "--" + name + "=" + content
		} else {
			out[at] = content
		}
		if spec.Bundle != nil {
			for _, value := range bundleValues(images) {
				extra = append(extra, "--"+spec.Bundle.Flag, value)
			}
		}
	}
	return append(out, extra...), nil
}

// ReadClientFile reads a file under a ClientFile flag's limits. For a CLIENT_LOCAL leaf, whose process is the
// caller's machine; a runtime-executed leaf receives the content already (ExpandClientFiles).
func ReadClientFile(flag Flag, path string) (string, *Error) { return readClientFile(flag, path) }

func readClientFile(flag Flag, path string) (string, *Error) {
	data, err := readLimited(flag, path, true)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readLimited reads a file under the flag's limits; text demands valid UTF-8.
func readLimited(flag Flag, path string, text bool) ([]byte, *Error) {
	refuse := func(code, format string, args ...interface{}) *Error {
		return UsageError(code, format, args...).WithContextValue("flag", flag.Name)
	}
	path = filepath.Clean(strings.TrimSpace(path))
	extension := strings.ToLower(filepath.Ext(path))
	allowed := false
	for _, want := range flag.ClientFile.Extensions {
		if extension == want {
			allowed = true
		}
	}
	if !allowed {
		return nil, refuse("LOCAL_FILE_TYPE", "--%s must name a %s file on this machine", flag.Name, strings.Join(flag.ClientFile.Extensions, " or ")).
			WithRemediation("Only files of these types are read and sent; anything else stays on this machine.")
	}
	// Lstat: a symbolic link is refused, so a link named like an image cannot carry another file out.
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, refuse("LOCAL_FILE_UNREADABLE", "could not read --%s %s", flag.Name, path)
	case !info.Mode().IsRegular():
		return nil, refuse("LOCAL_FILE_UNREADABLE", "--%s %s is not a regular file", flag.Name, path)
	case info.Size() > flag.ClientFile.MaxBytes:
		return nil, refuse("LOCAL_FILE_TOO_LARGE", "--%s %s is larger than %d bytes", flag.Name, path, flag.ClientFile.MaxBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, refuse("LOCAL_FILE_UNREADABLE", "could not read --%s %s", flag.Name, path)
	}
	if text && !utf8.Valid(content) {
		return nil, refuse("LOCAL_FILE_NOT_TEXT", "--%s %s is not UTF-8 text", flag.Name, path)
	}
	return content, nil
}
