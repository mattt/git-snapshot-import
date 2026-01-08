package snapshot_test

import (
	"bytes"
	"io"
	"io/fs"
	"strings"
	"time"
)

// ============================================================================
// MOCK FILESYSTEM
// ============================================================================

// MockFS is an in-memory filesystem for testing
type MockFS struct {
	Files map[string][]byte // path -> content
	Dirs  map[string]bool   // directories that exist
	Err   error             // error to return (for testing error paths)
}

func NewMockFS() *MockFS {
	return &MockFS{
		Files: make(map[string][]byte),
		Dirs:  make(map[string]bool),
	}
}

func (m *MockFS) AddDir(path string) {
	m.Dirs[path] = true
}

func (m *MockFS) AddFile(path string, content []byte) {
	m.Files[path] = content
	// Ensure parent dirs exist
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		m.Dirs[strings.Join(parts[:i], "/")] = true
	}
}

func (m *MockFS) Stat(name string) (fs.FileInfo, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if m.Dirs[name] {
		return &mockFileInfo{name: name, isDir: true}, nil
	}
	if _, ok := m.Files[name]; ok {
		return &mockFileInfo{name: name, isDir: false}, nil
	}
	return nil, fs.ErrNotExist
}

func (m *MockFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	var entries []fs.DirEntry
	seen := make(map[string]bool)

	prefix := name + "/"

	// Find directories
	for path := range m.Dirs {
		if strings.HasPrefix(path, prefix) {
			rel := strings.TrimPrefix(path, prefix)
			if idx := strings.Index(rel, "/"); idx >= 0 {
				rel = rel[:idx]
			}
			if rel != "" && !seen[rel] {
				seen[rel] = true
				entries = append(entries, &mockDirEntry{name: rel, isDir: true})
			}
		}
	}

	// Find files
	for path := range m.Files {
		if strings.HasPrefix(path, prefix) {
			rel := strings.TrimPrefix(path, prefix)
			if !strings.Contains(rel, "/") && !seen[rel] {
				seen[rel] = true
				entries = append(entries, &mockDirEntry{name: rel, isDir: false})
			}
		}
	}

	return entries, nil
}

func (m *MockFS) ReadFile(name string) ([]byte, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if data, ok := m.Files[name]; ok {
		return data, nil
	}
	return nil, fs.ErrNotExist
}

func (m *MockFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if m.Err != nil {
		return m.Err
	}
	m.Files[name] = data
	return nil
}

func (m *MockFS) MkdirAll(path string, perm fs.FileMode) error {
	if m.Err != nil {
		return m.Err
	}
	m.Dirs[path] = true
	return nil
}

func (m *MockFS) RemoveAll(path string) error {
	if m.Err != nil {
		return m.Err
	}
	delete(m.Dirs, path)
	for k := range m.Files {
		if strings.HasPrefix(k, path+"/") || k == path {
			delete(m.Files, k)
		}
	}
	for k := range m.Dirs {
		if strings.HasPrefix(k, path+"/") {
			delete(m.Dirs, k)
		}
	}
	return nil
}

func (m *MockFS) Remove(name string) error {
	if m.Err != nil {
		return m.Err
	}
	delete(m.Files, name)
	delete(m.Dirs, name)
	return nil
}

func (m *MockFS) Open(name string) (io.ReadCloser, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if data, ok := m.Files[name]; ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil, fs.ErrNotExist
}

func (m *MockFS) Create(name string) (io.WriteCloser, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &mockWriteCloser{fs: m, path: name}, nil
}

func (m *MockFS) Walk(root string, fn fs.WalkDirFunc) error {
	if m.Err != nil {
		return m.Err
	}

	// Walk root first
	if err := fn(root, &mockDirEntry{name: root, isDir: true}, nil); err != nil {
		return err
	}

	prefix := root + "/"

	// Walk files
	for path := range m.Files {
		if strings.HasPrefix(path, prefix) {
			rel := strings.TrimPrefix(path, prefix)
			if err := fn(path, &mockDirEntry{name: rel, isDir: false}, nil); err != nil {
				if err == fs.SkipDir {
					continue
				}
				return err
			}
		}
	}

	return nil
}

// mockFileInfo implements fs.FileInfo
type mockFileInfo struct {
	name  string
	isDir bool
	mode  fs.FileMode
}

func (m *mockFileInfo) Name() string      { return m.name }
func (m *mockFileInfo) Size() int64       { return 0 }
func (m *mockFileInfo) Mode() fs.FileMode { if m.isDir { return fs.ModeDir | 0755 }; return 0644 }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }

// mockDirEntry implements fs.DirEntry
type mockDirEntry struct {
	name  string
	isDir bool
}

func (m *mockDirEntry) Name() string               { return m.name }
func (m *mockDirEntry) IsDir() bool                { return m.isDir }
func (m *mockDirEntry) Type() fs.FileMode          { if m.isDir { return fs.ModeDir }; return 0 }
func (m *mockDirEntry) Info() (fs.FileInfo, error) { return &mockFileInfo{name: m.name, isDir: m.isDir}, nil }

// mockWriteCloser captures writes for testing
type mockWriteCloser struct {
	fs   *MockFS
	path string
	buf  bytes.Buffer
}

func (m *mockWriteCloser) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *mockWriteCloser) Close() error {
	m.fs.Files[m.path] = m.buf.Bytes()
	return nil
}

// ============================================================================
// MOCK GIT RUNNER
// ============================================================================

// MockGit records git commands for verification
type MockGit struct {
	Commands []GitCommand
	Err      error // error to return (for testing error paths)
}

type GitCommand struct {
	Dir  string
	Args []string
	Env  []string
}

func (m *MockGit) Run(dir string, args ...string) error {
	return m.RunWithEnv(dir, nil, args...)
}

func (m *MockGit) RunWithEnv(dir string, env []string, args ...string) error {
	if m.Err != nil {
		return m.Err
	}
	m.Commands = append(m.Commands, GitCommand{Dir: dir, Args: args, Env: env})
	return nil
}

