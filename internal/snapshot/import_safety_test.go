package snapshot_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mattt/git-snapshot-import/internal/snapshot"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func checkFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("read %s: got %q, %v; want %q", path, got, err, want)
	}
}

func TestExecuteRejectsOverlap(t *testing.T) {
	for _, tc := range []struct{ name, source, target, absent string }{
		{"same", "source", "source", ""},
		{"ancestor", "source", ".", ""},
		{"child", "source", "source/output", "source/output"},
		{"source alias", "alias", "source", ""},
		{"target alias", "source", "alias", ""},
		{"case alias", "source", "SOURCE", ""},
		{"missing below alias", "source", "alias/missing/output", "source/missing"},
		{"symlink before dot-dot", "source", "nested/../output", "source/output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			file := filepath.Join(source, "2000-01-01", "file")
			writeFixture(t, file, "original")
			alias := filepath.Join(root, "alias")
			if err := os.Symlink(source, alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Dir(file), filepath.Join(root, "nested")); err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			if tc.target == "SOURCE" {
				if _, err := os.Stat(tc.target); errors.Is(err, os.ErrNotExist) {
					t.Skip("fixture filesystem is case-sensitive")
				}
			}
			plan := &snapshot.Plan{SourceDir: tc.source, TargetDir: tc.target}
			git := &MockGit{}
			imp := snapshot.New(snapshot.OSFileSystem{}, git, io.Discard, io.Discard)
			err := imp.Execute(plan, snapshot.ImportOptions{Force: true})
			if err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Errorf("expected overlap error, got %v", err)
			}
			if len(git.Commands) != 0 {
				t.Error("Git ran before overlap check")
			}
			checkFile(t, file, "original")
			if _, err := os.Readlink(alias); err != nil {
				t.Error(err)
			}
			if tc.absent != "" {
				if _, err := os.Stat(tc.absent); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("unexpected target directory %s: %v", tc.absent, err)
				}
			}
		})
	}
}

func TestInterruptedImportRejectsOverlap(t *testing.T) {
	for _, command := range []string{"continue", "abort"} {
		t.Run(command, func(t *testing.T) {
			target := t.TempDir()
			source := filepath.Join(target, "source")
			name := "2000-01-01"
			file := filepath.Join(source, name, "file")
			writeFixture(t, file, "original")
			state := snapshot.State{SourceDir: source, TargetDir: target, FullSnapshots: true, PendingNames: []string{name}}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			stateFile := filepath.Join(target, ".git", snapshot.StateFile)
			writeFixture(t, stateFile, string(data))
			imp := snapshot.New(snapshot.OSFileSystem{}, &MockGit{}, io.Discard, io.Discard)
			if command == "abort" {
				err = imp.Abort(target)
			} else {
				err = imp.Continue(target, snapshot.ImportOptions{})
			}
			if err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Errorf("expected overlap error, got %v", err)
			}
			checkFile(t, file, "original")
			checkFile(t, stateFile, string(data))
		})
	}
}

func TestContinueRetriesFailedSnapshot(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	source := filepath.Join(root, "snapshots")
	target := filepath.Join(root, "snapshots-git")
	names := []string{"2000-01-01", "2000-01-02", "2000-01-03"}
	for _, name := range names {
		writeFixture(t, filepath.Join(source, name, "file"), name)
	}

	imp := snapshot.NewDefault(io.Discard, io.Discard)
	opts := snapshot.ImportOptions{Branch: "main", DefaultAuthor: "Test", DefaultEmail: "test@example.invalid"}
	plan, err := imp.CreatePlan(source, target, opts)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(target, ".git", "index.lock")
	imp.Progress = func(_, _ int, name string) {
		if name == names[1] {
			writeFixture(t, lock, "")
		}
	}
	if err := imp.Execute(plan, opts); err == nil {
		t.Fatal("expected import to fail with locked index")
	}
	imp.Progress = nil
	if err := imp.Continue(target, opts); err == nil {
		t.Fatal("expected resume to fail with locked index")
	}

	stateFile := filepath.Join(target, ".git", snapshot.StateFile)
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var state snapshot.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.CompletedNames, names[:1]) || !slices.Equal(state.PendingNames, names[1:]) {
		t.Errorf("after failure: completed %v, pending %v", state.CompletedNames, state.PendingNames)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	// The failed snapshot must still be pending if it is missing on retry.
	missing := filepath.Join(source, names[1])
	backup := filepath.Join(root, "backup")
	if err := os.Rename(missing, backup); err != nil {
		t.Fatal(err)
	}
	if err := imp.Continue(target, opts); err == nil {
		t.Error("expected resume to fail with a missing pending snapshot")
	}
	checkFile(t, stateFile, string(data))
	if err := os.Rename(backup, missing); err != nil {
		t.Fatal(err)
	}
	if err := imp.Continue(target, opts); err != nil {
		t.Fatal(err)
	}
	log, err := exec.Command("git", "-C", target, "log", "--reverse", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, name := range names {
		subjects = append(subjects, "Snapshot from "+name)
		checkFile(t, filepath.Join(source, name, "file"), name)
	}
	if !slices.Equal(strings.Split(strings.TrimSpace(string(log)), "\n"), subjects) {
		t.Errorf("unexpected Git history: %s", log)
	}
	if _, err := os.Stat(stateFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("resume state still present: %v", err)
	}
}
