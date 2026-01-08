package snapshot

import "time"

const (
	// MetaFile is the name of the metadata file in each snapshot directory
	MetaFile = ".git-snapshot.json"

	// StateFile is the name of the import state file for --continue/--abort
	StateFile = ".git-snapshot-import-state.json"
)

// Meta holds the metadata for a single snapshot
type Meta struct {
	Author  string   `json:"author,omitempty"`
	Email   string   `json:"email,omitempty"`
	Message string   `json:"message,omitempty"`
	Deleted []string `json:"deleted,omitempty"` // Files to remove (tombstones)
}

// Snapshot represents a parsed directory snapshot
type Snapshot struct {
	Path      string
	Name      string
	Timestamp time.Time
	Meta      Meta
}

// Plan represents a validated, ready-to-execute import operation
type Plan struct {
	SourceDir string
	TargetDir string
	Branch    string
	Snapshots []Snapshot
	Warnings  []string
}

// State tracks progress for --continue/--abort
type State struct {
	SourceDir      string   `json:"source_dir"`
	TargetDir      string   `json:"target_dir"`
	Branch         string   `json:"branch"`
	FullSnapshots  bool     `json:"full_snapshots"`
	TotalSnapshots int      `json:"total_snapshots"`
	CompletedNames []string `json:"completed_names"`
	PendingNames   []string `json:"pending_names"`
}

// ImportOptions holds options for the import operation
type ImportOptions struct {
	DryRun        bool
	Force         bool
	Branch        string
	DefaultAuthor string
	DefaultEmail  string
	FullSnapshots bool // If true, don't carry forward files between snapshots (--no-incremental)
}

// InitOptions holds options for the init operation
type InitOptions struct {
	Overwrite bool
	Author    string
	Email     string
	Message   string
}
