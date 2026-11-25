package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Importer orchestrates the import process with injectable dependencies
type Importer struct {
	FS     FileSystem
	Git    GitRunner
	Stdout io.Writer
	Stderr io.Writer

	// Progress is called during import to report progress (optional)
	Progress func(current, total int, name string)
}

// New creates an Importer with the given dependencies
func New(fs FileSystem, git GitRunner, stdout, stderr io.Writer) *Importer {
	return &Importer{
		FS:     fs,
		Git:    git,
		Stdout: stdout,
		Stderr: stderr,
	}
}

// NewDefault creates an Importer with real OS and Git implementations
func NewDefault(stdout, stderr io.Writer) *Importer {
	return New(OSFileSystem{}, ExecGitRunner{}, stdout, stderr)
}

// CreatePlan validates the source directory and creates an execution plan
func (imp *Importer) CreatePlan(sourceDir, targetDir string, opts ImportOptions) (*Plan, error) {
	// Validate source directory
	info, err := imp.FS.Stat(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("cannot access source directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source path is not a directory: %s", sourceDir)
	}

	// Scan for snapshots
	snapshots, warnings, err := imp.scanSnapshots(sourceDir, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to scan snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no valid timestamped directories found in %s", sourceDir)
	}

	// Sort chronologically
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.Before(snapshots[j].Timestamp)
	})

	return &Plan{
		SourceDir: sourceDir,
		TargetDir: targetDir,
		Branch:    opts.Branch,
		Snapshots: snapshots,
		Warnings:  warnings,
	}, nil
}

// Execute runs an import plan, creating Git commits
func (imp *Importer) Execute(plan *Plan, opts ImportOptions) error {
	if err := imp.checkOverlap(plan.SourceDir, plan.TargetDir); err != nil {
		return err
	}

	// Check target directory
	if _, err := imp.FS.Stat(plan.TargetDir); err == nil {
		if !opts.Force {
			return fmt.Errorf("target directory already exists: %s (use --force to overwrite)", plan.TargetDir)
		}
		if err := imp.FS.RemoveAll(plan.TargetDir); err != nil {
			return fmt.Errorf("failed to remove existing target: %w", err)
		}
	}

	// Initialize repository
	if err := imp.initRepo(plan.TargetDir, plan.Branch); err != nil {
		return fmt.Errorf("failed to initialize repository: %w", err)
	}

	// Save initial state for --continue support
	state := &State{
		SourceDir:      plan.SourceDir,
		TargetDir:      plan.TargetDir,
		Branch:         plan.Branch,
		FullSnapshots:  opts.FullSnapshots,
		TotalSnapshots: len(plan.Snapshots),
	}
	for _, s := range plan.Snapshots {
		state.PendingNames = append(state.PendingNames, s.Name)
	}

	// Process each snapshot
	total := len(plan.Snapshots)
	for i, snapshot := range plan.Snapshots {
		if imp.Progress != nil {
			imp.Progress(i, total, snapshot.Name)
		}

		if err := imp.commitSnapshot(plan.TargetDir, snapshot, opts.FullSnapshots); err != nil {
			// Save state for --continue
			if saveErr := imp.saveState(plan.TargetDir, state); saveErr != nil {
				fmt.Fprintf(imp.Stderr, "warning: failed to save state: %v\n", saveErr)
			}
			return fmt.Errorf("failed to commit snapshot %s: %w", snapshot.Name, err)
		}

		// Update state
		state.CompletedNames = append(state.CompletedNames, snapshot.Name)
		state.PendingNames = state.PendingNames[1:]
	}

	// Clean up state file on success
	_ = imp.FS.Remove(filepath.Join(plan.TargetDir, ".git", StateFile))

	return nil
}

// Continue resumes an interrupted import
func (imp *Importer) Continue(targetDir string, opts ImportOptions) error {
	state, err := imp.loadState(targetDir)
	if err != nil {
		return fmt.Errorf("no interrupted import found: %w", err)
	}

	if err := imp.checkOverlap(state.SourceDir, targetDir); err != nil {
		return err
	}

	fmt.Fprintf(imp.Stdout, "Resuming import: %d completed, %d remaining\n",
		len(state.CompletedNames), len(state.PendingNames))

	// Rebuild snapshots for pending items
	allSnapshots, _, err := imp.scanSnapshots(state.SourceDir, opts)
	if err != nil {
		return err
	}

	// Use the saved order and check that every pending snapshot is available.
	byName := make(map[string]Snapshot, len(allSnapshots))
	for _, snapshot := range allSnapshots {
		byName[snapshot.Name] = snapshot
	}
	var pending []Snapshot
	for _, name := range state.PendingNames {
		snapshot, ok := byName[name]
		if !ok {
			return fmt.Errorf("pending snapshot %s is missing from %s", name, state.SourceDir)
		}
		pending = append(pending, snapshot)
	}

	// Process remaining
	total := state.TotalSnapshots
	completed := len(state.CompletedNames)

	for i, snapshot := range pending {
		if imp.Progress != nil {
			imp.Progress(completed+i, total, snapshot.Name)
		}

		if err := imp.commitSnapshot(targetDir, snapshot, state.FullSnapshots); err != nil {
			if saveErr := imp.saveState(targetDir, state); saveErr != nil {
				fmt.Fprintf(imp.Stderr, "warning: failed to save state: %v\n", saveErr)
			}
			return fmt.Errorf("failed to commit snapshot %s: %w", snapshot.Name, err)
		}

		state.CompletedNames = append(state.CompletedNames, snapshot.Name)
		state.PendingNames = state.PendingNames[1:]
	}

	_ = imp.FS.Remove(filepath.Join(targetDir, ".git", StateFile))
	return nil
}

// Abort cleans up an interrupted import
func (imp *Importer) Abort(targetDir string) error {
	state, err := imp.loadState(targetDir)
	if err != nil {
		return fmt.Errorf("no interrupted import found: %w", err)
	}

	if err := imp.checkOverlap(state.SourceDir, targetDir); err != nil {
		return err
	}

	// Remove the entire target directory
	if err := imp.FS.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("failed to remove target directory: %w", err)
	}

	fmt.Fprintf(imp.Stdout, "Aborted import. Removed %s (%d of %d commits were completed)\n",
		targetDir, len(state.CompletedNames), state.TotalSnapshots)

	return nil
}

// Init generates template metadata files
func (imp *Importer) Init(sourceDir string, opts InitOptions) (created, skipped int, err error) {
	info, err := imp.FS.Stat(sourceDir)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot access source directory: %w", err)
	}
	if !info.IsDir() {
		return 0, 0, fmt.Errorf("source path is not a directory: %s", sourceDir)
	}

	entries, err := imp.FS.ReadDir(sourceDir)
	if err != nil {
		return 0, 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if _, err := ParseTimestamp(name); err != nil {
			continue
		}

		metaPath := filepath.Join(sourceDir, name, MetaFile)

		if _, err := imp.FS.Stat(metaPath); err == nil {
			if !opts.Overwrite {
				skipped++
				continue
			}
		}

		meta := Meta{
			Author:  opts.Author,
			Email:   opts.Email,
			Message: opts.Message,
		}
		if meta.Message == "" {
			meta.Message = fmt.Sprintf("Snapshot from %s", name)
		}

		data, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return created, skipped, fmt.Errorf("failed to marshal JSON for %s: %w", name, err)
		}

		if err := imp.FS.WriteFile(metaPath, data, 0644); err != nil {
			return created, skipped, fmt.Errorf("failed to write %s: %w", metaPath, err)
		}

		created++
	}

	return created, skipped, nil
}

// scanSnapshots finds all valid timestamped directories
func (imp *Importer) scanSnapshots(sourceDir string, opts ImportOptions) ([]Snapshot, []string, error) {
	entries, err := imp.FS.ReadDir(sourceDir)
	if err != nil {
		return nil, nil, err
	}

	var snapshots []Snapshot
	var warnings []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		timestamp, err := ParseTimestamp(name)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v (skipped)", name, err))
			continue
		}

		snapshotPath := filepath.Join(sourceDir, name)
		meta, err := imp.loadMeta(snapshotPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			warnings = append(warnings, fmt.Sprintf("%s: %v (using defaults)", name, err))
		}

		// Apply defaults
		if meta.Author == "" {
			meta.Author = opts.DefaultAuthor
		}
		if meta.Email == "" {
			meta.Email = opts.DefaultEmail
		}
		if meta.Message == "" {
			meta.Message = fmt.Sprintf("Snapshot from %s", name)
		}

		// Check for empty directories
		subEntries, _ := imp.FS.ReadDir(snapshotPath)
		contentCount := 0
		for _, se := range subEntries {
			if se.Name() != MetaFile && !strings.HasPrefix(se.Name(), ".") {
				contentCount++
			}
		}
		if contentCount == 0 {
			warnings = append(warnings, fmt.Sprintf("%s: contains no files", name))
		}

		snapshots = append(snapshots, Snapshot{
			Path:      snapshotPath,
			Name:      name,
			Timestamp: timestamp,
			Meta:      meta,
		})
	}

	return snapshots, warnings, nil
}

// loadMeta reads the snapshot metadata file
func (imp *Importer) loadMeta(snapshotPath string) (Meta, error) {
	metaPath := filepath.Join(snapshotPath, MetaFile)
	data, err := imp.FS.ReadFile(metaPath)
	if err != nil {
		return Meta{}, err
	}

	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, fmt.Errorf("invalid JSON: %w", err)
	}

	return meta, nil
}

// initRepo creates and initializes a new Git repository
func (imp *Importer) initRepo(targetDir, branch string) error {
	if err := imp.FS.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	if err := imp.Git.Run(targetDir, "init", "-b", branch); err != nil {
		return err
	}

	if err := imp.Git.Run(targetDir, "config", "user.name", "git-snapshot-import"); err != nil {
		return err
	}
	if err := imp.Git.Run(targetDir, "config", "user.email", "import@example.com"); err != nil {
		return err
	}

	return nil
}

// commitSnapshot creates a commit from a snapshot directory
func (imp *Importer) commitSnapshot(targetDir string, snapshot Snapshot, fullSnapshot bool) error {
	if fullSnapshot {
		// Full snapshot mode: clear the working directory (except .git)
		entries, err := imp.FS.ReadDir(targetDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".git" {
				continue
			}
			path := filepath.Join(targetDir, entry.Name())
			if err := imp.FS.RemoveAll(path); err != nil {
				return fmt.Errorf("failed to clean %s: %w", path, err)
			}
		}
	} else {
		// Incremental mode: process tombstones (deletions)
		for _, deleted := range snapshot.Meta.Deleted {
			path := filepath.Join(targetDir, deleted)
			if err := imp.FS.RemoveAll(path); err != nil {
				// Ignore errors for files that don't exist
				if !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("failed to delete %s: %w", deleted, err)
				}
			}
		}
	}

	// Copy snapshot contents (overlay in incremental mode, replace in full mode)
	if err := imp.copyDir(snapshot.Path, targetDir); err != nil {
		return fmt.Errorf("failed to copy snapshot: %w", err)
	}

	// Stage all changes
	if err := imp.Git.Run(targetDir, "add", "-A"); err != nil {
		return err
	}

	// Create commit with backdated timestamp
	dateStr := snapshot.Timestamp.Format(time.RFC3339)
	env := []string{
		fmt.Sprintf("GIT_AUTHOR_DATE=%s", dateStr),
		fmt.Sprintf("GIT_COMMITTER_DATE=%s", dateStr),
		fmt.Sprintf("GIT_AUTHOR_NAME=%s", snapshot.Meta.Author),
		fmt.Sprintf("GIT_AUTHOR_EMAIL=%s", snapshot.Meta.Email),
		fmt.Sprintf("GIT_COMMITTER_NAME=%s", snapshot.Meta.Author),
		fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", snapshot.Meta.Email),
	}

	if err := imp.Git.RunWithEnv(targetDir, env, "commit", "--allow-empty", "-m", snapshot.Meta.Message); err != nil {
		return err
	}

	return nil
}

// copyDir recursively copies a directory, excluding metadata and hidden files
func (imp *Importer) copyDir(src, dst string) error {
	return imp.FS.Walk(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		// Skip metadata file
		if relPath == MetaFile {
			return nil
		}

		// Skip hidden files/directories
		base := filepath.Base(relPath)
		if strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		dstPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return imp.FS.MkdirAll(dstPath, info.Mode())
		}

		return imp.copyFile(path, dstPath)
	})
}

// copyFile copies a single file
func (imp *Importer) copyFile(src, dst string) error {
	srcFile, err := imp.FS.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := imp.FS.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// saveState persists import state for --continue
func (imp *Importer) saveState(targetDir string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return imp.FS.WriteFile(filepath.Join(targetDir, ".git", StateFile), data, 0644)
}

// loadState reads import state for --continue
func (imp *Importer) loadState(targetDir string) (*State, error) {
	data, err := imp.FS.ReadFile(filepath.Join(targetDir, ".git", StateFile))
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}
