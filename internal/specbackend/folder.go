package specbackend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// folderStore keeps a backend's objects in a folder: on this machine, or one a team shares (a
// network drive, a synced folder whose sync keeps creates exclusive). A lock is a file created with
// O_EXCL.
type folderStore struct{ root string }

func (f *folderStore) String() string { return f.root }

func (f *folderStore) path(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("specbackend: %q is outside the backend", name)
	}
	return filepath.Join(f.root, clean), nil
}

func (f *folderStore) Get(_ context.Context, name string) ([]byte, error) {
	path, err := f.path(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("%w: %v", ErrDenied, err)
	}
	return data, err
}

func (f *folderStore) Put(_ context.Context, name string, data []byte) error {
	path, err := f.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp-" + NewID()
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (f *folderStore) Create(_ context.Context, name string, data []byte) error {
	path, err := f.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	if _, err := handle.Write(data); err != nil {
		_ = handle.Close()
		_ = os.Remove(path)
		return err
	}
	return handle.Close()
}

func (f *folderStore) Delete(_ context.Context, name string) error {
	path, err := f.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (f *folderStore) List(_ context.Context, folder string) ([]Object, error) {
	path, err := f.path(folder)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, entry := range entries {
		if entry.IsDir() || strings.Contains(entry.Name(), ".tmp-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, Object{Name: strings.TrimSuffix(folder, "/") + "/" + entry.Name(), Modified: info.ModTime(), Size: info.Size()})
	}
	return out, nil
}
