package snapshot

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// FileSystem abstracts filesystem operations for testing
type FileSystem interface {
	EvalSymlinks(path string) (string, error)
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	RemoveAll(path string) error
	Remove(name string) error
	Open(name string) (io.ReadCloser, error)
	Create(name string) (io.WriteCloser, error)
	Walk(root string, fn fs.WalkDirFunc) error
}

// GitRunner abstracts Git command execution for testing
type GitRunner interface {
	Run(dir string, args ...string) error
	RunWithEnv(dir string, env []string, args ...string) error
}

// OSFileSystem implements FileSystem using the real OS
type OSFileSystem struct{}

func (OSFileSystem) EvalSymlinks(path string) (string, error)          { return filepath.EvalSymlinks(path) }
func (OSFileSystem) Stat(name string) (fs.FileInfo, error)             { return os.Stat(name) }
func (OSFileSystem) ReadDir(name string) ([]fs.DirEntry, error)        { return os.ReadDir(name) }
func (OSFileSystem) ReadFile(name string) ([]byte, error)              { return os.ReadFile(name) }
func (OSFileSystem) WriteFile(n string, d []byte, p fs.FileMode) error { return os.WriteFile(n, d, p) }
func (OSFileSystem) MkdirAll(path string, perm fs.FileMode) error      { return os.MkdirAll(path, perm) }
func (OSFileSystem) RemoveAll(path string) error                       { return os.RemoveAll(path) }
func (OSFileSystem) Remove(name string) error                          { return os.Remove(name) }
func (OSFileSystem) Open(name string) (io.ReadCloser, error)           { return os.Open(name) }
func (OSFileSystem) Create(name string) (io.WriteCloser, error)        { return os.Create(name) }
func (OSFileSystem) Walk(root string, fn fs.WalkDirFunc) error         { return filepath.WalkDir(root, fn) }

// ExecGitRunner implements GitRunner using exec.Command
type ExecGitRunner struct{}

func (ExecGitRunner) Run(dir string, args ...string) error {
	return ExecGitRunner{}.RunWithEnv(dir, nil, args...)
}

func (ExecGitRunner) RunWithEnv(dir string, env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &GitError{Args: args, Output: string(output), Err: err}
	}
	return nil
}

// GitError wraps git command errors with context
type GitError struct {
	Args   []string
	Output string
	Err    error
}

func (e *GitError) Error() string {
	return e.Err.Error() + ": " + e.Output
}

func (e *GitError) Unwrap() error {
	return e.Err
}
