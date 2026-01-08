package snapshot_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattt/git-snapshot-import/internal/snapshot"
)

func TestCreatePlan_SortsChronologically(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1791-12-15")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddDir("/source/1788-06-21")

	// Add a file to each so they're not empty
	mockFS.AddFile("/source/1791-12-15/doc.txt", []byte("bill of rights"))
	mockFS.AddFile("/source/1787-09-17/doc.txt", []byte("constitution draft"))
	mockFS.AddFile("/source/1788-06-21/doc.txt", []byte("ratification"))

	imp := snapshot.New(mockFS, &MockGit{}, &bytes.Buffer{}, &bytes.Buffer{})

	plan, err := imp.CreatePlan("/source", "/target", snapshot.ImportOptions{
		Branch:        "main",
		DefaultAuthor: "Test Author",
		DefaultEmail:  "test@example.com",
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	if len(plan.Snapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(plan.Snapshots))
	}

	// Should be sorted chronologically
	expected := []string{"1787-09-17", "1788-06-21", "1791-12-15"}
	for i, name := range expected {
		if plan.Snapshots[i].Name != name {
			t.Errorf("snapshot %d: got %s, want %s", i, plan.Snapshots[i].Name, name)
		}
	}
}

func TestCreatePlan_LoadsMetadata(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddFile("/source/1787-09-17/constitution.txt", []byte("We the People..."))

	meta := snapshot.Meta{
		Author:  "James Madison",
		Email:   "jmadison@founders.gov",
		Message: "Initial draft",
	}
	metaJSON, _ := json.Marshal(meta)
	mockFS.AddFile("/source/1787-09-17/.git-snapshot.json", metaJSON)

	imp := snapshot.New(mockFS, &MockGit{}, &bytes.Buffer{}, &bytes.Buffer{})

	plan, err := imp.CreatePlan("/source", "/target", snapshot.ImportOptions{
		Branch:        "main",
		DefaultAuthor: "Default Author",
		DefaultEmail:  "default@example.com",
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	if plan.Snapshots[0].Meta.Author != "James Madison" {
		t.Errorf("expected author 'James Madison', got '%s'", plan.Snapshots[0].Meta.Author)
	}
	if plan.Snapshots[0].Meta.Message != "Initial draft" {
		t.Errorf("expected message 'Initial draft', got '%s'", plan.Snapshots[0].Meta.Message)
	}
}

func TestCreatePlan_UsesDefaultsWhenNoMeta(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddFile("/source/1787-09-17/constitution.txt", []byte("We the People..."))

	imp := snapshot.New(mockFS, &MockGit{}, &bytes.Buffer{}, &bytes.Buffer{})

	plan, err := imp.CreatePlan("/source", "/target", snapshot.ImportOptions{
		Branch:        "main",
		DefaultAuthor: "Default Author",
		DefaultEmail:  "default@example.com",
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	if plan.Snapshots[0].Meta.Author != "Default Author" {
		t.Errorf("expected author 'Default Author', got '%s'", plan.Snapshots[0].Meta.Author)
	}
}

func TestCreatePlan_SkipsInvalidTimestamps(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddDir("/source/not-a-date")
	mockFS.AddDir("/source/random-folder")
	mockFS.AddFile("/source/1787-09-17/doc.txt", []byte("content"))

	imp := snapshot.New(mockFS, &MockGit{}, &bytes.Buffer{}, &bytes.Buffer{})

	plan, err := imp.CreatePlan("/source", "/target", snapshot.ImportOptions{
		Branch:        "main",
		DefaultAuthor: "Test",
		DefaultEmail:  "test@example.com",
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	if len(plan.Snapshots) != 1 {
		t.Errorf("expected 1 valid snapshot, got %d", len(plan.Snapshots))
	}

	// Should have warnings about skipped dirs
	if len(plan.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(plan.Warnings), plan.Warnings)
	}
}

func TestExecute_CreatesGitCommands(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddFile("/source/1787-09-17/constitution.txt", []byte("We the People..."))

	mockGit := &MockGit{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	imp := snapshot.New(mockFS, mockGit, stdout, stderr)

	plan := &snapshot.Plan{
		SourceDir: "/source",
		TargetDir: "/target",
		Branch:    "main",
		Snapshots: []snapshot.Snapshot{
			{
				Path:      "/source/1787-09-17",
				Name:      "1787-09-17",
				Timestamp: time.Date(1787, 9, 17, 0, 0, 0, 0, time.UTC),
				Meta: snapshot.Meta{
					Author:  "James Madison",
					Email:   "jmadison@founders.gov",
					Message: "Initial draft",
				},
			},
		},
	}

	err := imp.Execute(plan, snapshot.ImportOptions{Branch: "main"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify git commands were called
	if len(mockGit.Commands) < 4 {
		t.Fatalf("expected at least 4 git commands, got %d", len(mockGit.Commands))
	}

	// First should be init
	if mockGit.Commands[0].Args[0] != "init" {
		t.Errorf("first command should be init, got %v", mockGit.Commands[0].Args)
	}

	// Should have a commit with environment variables
	var commitCmd *GitCommand
	for i := range mockGit.Commands {
		if len(mockGit.Commands[i].Args) > 0 && mockGit.Commands[i].Args[0] == "commit" {
			commitCmd = &mockGit.Commands[i]
			break
		}
	}

	if commitCmd == nil {
		t.Fatal("no commit command found")
	}

	// Check environment variables
	hasAuthorDate := false
	hasAuthorName := false
	for _, env := range commitCmd.Env {
		if strings.HasPrefix(env, "GIT_AUTHOR_DATE=") {
			hasAuthorDate = true
		}
		if strings.HasPrefix(env, "GIT_AUTHOR_NAME=James Madison") {
			hasAuthorName = true
		}
	}

	if !hasAuthorDate {
		t.Error("commit missing GIT_AUTHOR_DATE")
	}
	if !hasAuthorName {
		t.Error("commit missing GIT_AUTHOR_NAME")
	}
}

func TestInit_CreatesMetadataFiles(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddDir("/source/1788-06-21")
	mockFS.AddFile("/source/1787-09-17/doc.txt", []byte("content"))
	mockFS.AddFile("/source/1788-06-21/doc.txt", []byte("content"))

	imp := snapshot.New(mockFS, &MockGit{}, &bytes.Buffer{}, &bytes.Buffer{})

	created, skipped, err := imp.Init("/source", snapshot.InitOptions{
		Author: "James Madison",
		Email:  "jmadison@founders.gov",
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if created != 2 {
		t.Errorf("expected 2 created, got %d", created)
	}
	if skipped != 0 {
		t.Errorf("expected 0 skipped, got %d", skipped)
	}

	// Verify files were created
	data, ok := mockFS.Files["/source/1787-09-17/.git-snapshot.json"]
	if !ok {
		t.Fatal("metadata file not created")
	}

	var meta snapshot.Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if meta.Author != "James Madison" {
		t.Errorf("expected author 'James Madison', got '%s'", meta.Author)
	}
}

func TestInit_SkipsExistingFiles(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddDir("/source/1788-06-21")
	mockFS.AddFile("/source/1787-09-17/doc.txt", []byte("content"))
	mockFS.AddFile("/source/1788-06-21/doc.txt", []byte("content"))
	mockFS.AddFile("/source/1787-09-17/.git-snapshot.json", []byte(`{"author":"Existing"}`))

	imp := snapshot.New(mockFS, &MockGit{}, &bytes.Buffer{}, &bytes.Buffer{})

	created, skipped, err := imp.Init("/source", snapshot.InitOptions{
		Author: "New Author",
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if created != 1 {
		t.Errorf("expected 1 created, got %d", created)
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", skipped)
	}

	// Verify existing file wasn't overwritten
	data := mockFS.Files["/source/1787-09-17/.git-snapshot.json"]
	var meta snapshot.Meta
	json.Unmarshal(data, &meta)
	if meta.Author != "Existing" {
		t.Errorf("existing file was overwritten")
	}
}

func TestExecute_IncrementalMode_CarriesForwardFiles(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")

	// First snapshot has two files
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddFile("/source/1787-09-17/preamble.txt", []byte("We the People"))
	mockFS.AddFile("/source/1787-09-17/articles.txt", []byte("Article I"))

	// Second snapshot only updates one file (incremental)
	mockFS.AddDir("/source/1787-09-28")
	mockFS.AddFile("/source/1787-09-28/articles.txt", []byte("Article I (revised)"))

	mockGit := &MockGit{}
	imp := snapshot.New(mockFS, mockGit, &bytes.Buffer{}, &bytes.Buffer{})

	plan := &snapshot.Plan{
		SourceDir: "/source",
		TargetDir: "/target",
		Branch:    "main",
		Snapshots: []snapshot.Snapshot{
			{
				Path:      "/source/1787-09-17",
				Name:      "1787-09-17",
				Timestamp: time.Date(1787, 9, 17, 0, 0, 0, 0, time.UTC),
				Meta:      snapshot.Meta{Author: "Madison", Email: "m@test.com", Message: "Initial"},
			},
			{
				Path:      "/source/1787-09-28",
				Name:      "1787-09-28",
				Timestamp: time.Date(1787, 9, 28, 0, 0, 0, 0, time.UTC),
				Meta:      snapshot.Meta{Author: "Madison", Email: "m@test.com", Message: "Revise articles"},
			},
		},
	}

	// Default: incremental mode (FullSnapshots = false)
	err := imp.Execute(plan, snapshot.ImportOptions{Branch: "main"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// After second commit, both files should exist in target
	if _, ok := mockFS.Files["/target/preamble.txt"]; !ok {
		t.Error("preamble.txt should be carried forward in incremental mode")
	}
	if _, ok := mockFS.Files["/target/articles.txt"]; !ok {
		t.Error("articles.txt should exist")
	}

	// articles.txt should have the updated content
	if string(mockFS.Files["/target/articles.txt"]) != "Article I (revised)" {
		t.Error("articles.txt should have updated content")
	}
}

func TestExecute_NoIncremental_ClearsFiles(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")

	// First snapshot has two files
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddFile("/source/1787-09-17/preamble.txt", []byte("We the People"))
	mockFS.AddFile("/source/1787-09-17/articles.txt", []byte("Article I"))

	// Second snapshot only has one file (in non-incremental mode, preamble should be gone)
	mockFS.AddDir("/source/1787-09-28")
	mockFS.AddFile("/source/1787-09-28/articles.txt", []byte("Article I (revised)"))

	mockGit := &MockGit{}
	imp := snapshot.New(mockFS, mockGit, &bytes.Buffer{}, &bytes.Buffer{})

	plan := &snapshot.Plan{
		SourceDir: "/source",
		TargetDir: "/target",
		Branch:    "main",
		Snapshots: []snapshot.Snapshot{
			{
				Path:      "/source/1787-09-17",
				Name:      "1787-09-17",
				Timestamp: time.Date(1787, 9, 17, 0, 0, 0, 0, time.UTC),
				Meta:      snapshot.Meta{Author: "Madison", Email: "m@test.com", Message: "Initial"},
			},
			{
				Path:      "/source/1787-09-28",
				Name:      "1787-09-28",
				Timestamp: time.Date(1787, 9, 28, 0, 0, 0, 0, time.UTC),
				Meta:      snapshot.Meta{Author: "Madison", Email: "m@test.com", Message: "Revise articles"},
			},
		},
	}

	// Non-incremental mode
	err := imp.Execute(plan, snapshot.ImportOptions{Branch: "main", FullSnapshots: true})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// After second commit, only articles.txt should exist
	if _, ok := mockFS.Files["/target/preamble.txt"]; ok {
		t.Error("preamble.txt should NOT exist in non-incremental mode")
	}
	if _, ok := mockFS.Files["/target/articles.txt"]; !ok {
		t.Error("articles.txt should exist")
	}
}

func TestExecute_Tombstones_DeleteFiles(t *testing.T) {
	mockFS := NewMockFS()
	mockFS.AddDir("/source")

	// First snapshot has two files
	mockFS.AddDir("/source/1787-09-17")
	mockFS.AddFile("/source/1787-09-17/preamble.txt", []byte("We the People"))
	mockFS.AddFile("/source/1787-09-17/articles.txt", []byte("Article I"))

	// Second snapshot deletes preamble via tombstone
	mockFS.AddDir("/source/1787-09-28")
	mockFS.AddFile("/source/1787-09-28/articles.txt", []byte("Article I (revised)"))

	mockGit := &MockGit{}
	imp := snapshot.New(mockFS, mockGit, &bytes.Buffer{}, &bytes.Buffer{})

	plan := &snapshot.Plan{
		SourceDir: "/source",
		TargetDir: "/target",
		Branch:    "main",
		Snapshots: []snapshot.Snapshot{
			{
				Path:      "/source/1787-09-17",
				Name:      "1787-09-17",
				Timestamp: time.Date(1787, 9, 17, 0, 0, 0, 0, time.UTC),
				Meta:      snapshot.Meta{Author: "Madison", Email: "m@test.com", Message: "Initial"},
			},
			{
				Path:      "/source/1787-09-28",
				Name:      "1787-09-28",
				Timestamp: time.Date(1787, 9, 28, 0, 0, 0, 0, time.UTC),
				Meta: snapshot.Meta{
					Author:  "Madison",
					Email:   "m@test.com",
					Message: "Remove preamble",
					Deleted: []string{"preamble.txt"}, // Tombstone!
				},
			},
		},
	}

	// Incremental mode with tombstone
	err := imp.Execute(plan, snapshot.ImportOptions{Branch: "main"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// preamble.txt should be deleted by tombstone
	if _, ok := mockFS.Files["/target/preamble.txt"]; ok {
		t.Error("preamble.txt should be deleted by tombstone")
	}
	if _, ok := mockFS.Files["/target/articles.txt"]; !ok {
		t.Error("articles.txt should exist")
	}
}

