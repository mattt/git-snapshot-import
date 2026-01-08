// git-snapshot-import converts timestamped directory snapshots into Git commits.
//
// This tool creates a Git interface to historical documents by translating
// directories named with ISO timestamps (e.g., 1787-09-17) into proper Git
// commits with equivalent dates and author information.
//
// Usage:
//
//	git-snapshot-import [target-dir] [flags]
//	git-snapshot-import init [flags]
//
// See --help for detailed usage information.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattt/git-snapshot-import/internal/snapshot"
)

const version = "1.0.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	cmd := args[0]

	switch cmd {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "git-snapshot-import version %s\n", version)
		return nil
	case "--continue":
		return runContinue(args[1:], stdout, stderr)
	case "--abort":
		return runAbort(args[1:], stdout, stderr)
	default:
		// Default to import command
		return runImport(args, stdout, stderr)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `git-snapshot-import - Convert timestamped directories to Git commits

USAGE:
    git-snapshot-import <source-dir> <target-dir> [flags]
    git-snapshot-import init <source-dir> [flags]
    git-snapshot-import --continue <target-dir>
    git-snapshot-import --abort <target-dir>

DESCRIPTION:
    Creates a new Git repository from timestamped snapshot directories.
    The source directory is preserved unchanged; files are copied to the
    target.

    By default, snapshots are incremental: unchanged files carry forward
    between commits. Only include files that changed. To delete a file,
    list it in the "deleted" field of .git-snapshot.json.

    Use --no-incremental if each directory contains a complete snapshot
    (all files must be present in every snapshot directory).

COMMANDS:
    (default)   Import snapshots into a new Git repository
    init        Generate template metadata files in source directory
    --continue  Resume an interrupted import
    --abort     Cancel an interrupted import and clean up
    help        Show this help message
    version     Show version information

FLAGS:
    -n, --dry-run         Show what would be done without making changes
    -f, --force           Overwrite target directory if it exists
    -b, --branch string   Branch name to create (default "main")
    --no-incremental      Don't carry forward files between snapshots
    --default-author      Default author name (default "Unknown Author")
    --default-email       Default email (default "unknown@example.com")

EXAMPLES:
    # Import incremental snapshots (default)
    git-snapshot-import ./constitution-drafts ./constitution-git

    # Import complete snapshots (each dir has all files)
    git-snapshot-import --no-incremental ./constitution-drafts ./constitution-git

    # Preview import without making changes
    git-snapshot-import --dry-run ./constitution-drafts ./constitution-git

    # Generate metadata templates (modifies source directory)
    git-snapshot-import init ./constitution-drafts --author "James Madison"

    # Resume after failure
    git-snapshot-import --continue ./constitution-git

SOURCE DIRECTORY STRUCTURE (preserved):
    source-dir/
        1787-09-17/                      # First snapshot: full content
            .git-snapshot.json
            preamble.txt
            articles.txt
        1787-09-28/                      # Only changed/new files needed
            .git-snapshot.json
            articles.txt                 # Updated file
        1788-06-21/                      # Delete a file using tombstone
            .git-snapshot.json           # Contains: "deleted": ["preamble.txt"]
            articles.txt

METADATA FILE FORMAT (.git-snapshot.json):
    {
        "author": "James Madison",
        "email": "jmadison@founders.gov",
        "message": "Revise Article III",
        "deleted": ["old-draft.txt", "notes/scratch.md"]
    }

    Fields:
        author   - Commit author name (optional, uses --default-author)
        email    - Commit author email (optional, uses --default-email)  
        message  - Commit message (optional, auto-generated from timestamp)
        deleted  - Files to remove in this snapshot (tombstones)

SUPPORTED TIMESTAMP FORMATS:
    1787-09-17                 Date only (midnight UTC)
    1787-09-17T16:00:00        With time
    1787-09-17T16:00:00Z       With UTC marker
    1787-09-17T11:00:00-05:00  With timezone offset
`)
}

func runImport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)

	opts := snapshot.ImportOptions{
		Branch:        "main",
		DefaultAuthor: "Unknown Author",
		DefaultEmail:  "unknown@example.com",
	}
	var dryRun bool

	fs.BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")
	fs.BoolVar(&dryRun, "n", false, "Dry run (shorthand)")
	fs.BoolVar(&opts.Force, "force", false, "Overwrite target directory if it exists")
	fs.BoolVar(&opts.Force, "f", false, "Force (shorthand)")
	fs.BoolVar(&opts.FullSnapshots, "no-incremental", false, "Don't carry forward files between snapshots")
	fs.StringVar(&opts.Branch, "branch", "main", "Branch name to create")
	fs.StringVar(&opts.Branch, "b", "main", "Branch name (shorthand)")
	fs.StringVar(&opts.DefaultAuthor, "default-author", "Unknown Author", "Default author name")
	fs.StringVar(&opts.DefaultEmail, "default-email", "unknown@example.com", "Default email")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 2 {
		printUsage(stdout)
		return fmt.Errorf("source and target directories required")
	}

	sourceDir := fs.Arg(0)
	targetDir := fs.Arg(1)

	imp := snapshot.NewDefault(stdout, stderr)
	imp.Progress = makeProgressFunc(stdout)

	plan, err := imp.CreatePlan(sourceDir, targetDir, opts)
	if err != nil {
		return err
	}

	// Print plan
	printPlan(stdout, plan)

	if dryRun {
		fmt.Fprintln(stdout, "Dry run — no changes made")
		return nil
	}

	if err := imp.Execute(plan, opts); err != nil {
		return err
	}

	// Clear progress line and show success
	fmt.Fprint(stdout, "\r\033[K")
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	fmt.Fprintf(stdout, "%s Imported %d commits to %s\n",
		successStyle.Render("✓"),
		len(plan.Snapshots),
		targetDir)

	return nil
}

func runContinue(args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("target directory required")
	}

	imp := snapshot.NewDefault(stdout, stderr)
	imp.Progress = makeProgressFunc(stdout)

	opts := snapshot.ImportOptions{
		DefaultAuthor: "Unknown Author",
		DefaultEmail:  "unknown@example.com",
	}

	if err := imp.Continue(args[0], opts); err != nil {
		return err
	}

	fmt.Fprint(stdout, "\r\033[K")
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	fmt.Fprintf(stdout, "%s Import resumed and completed\n", successStyle.Render("✓"))

	return nil
}

func runAbort(args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("target directory required")
	}

	imp := snapshot.NewDefault(stdout, stderr)
	return imp.Abort(args[0])
}

func runInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts snapshot.InitOptions

	fs.BoolVar(&opts.Overwrite, "overwrite", false, "Overwrite existing metadata files")
	fs.StringVar(&opts.Author, "author", "", "Default author name for all templates")
	fs.StringVar(&opts.Email, "email", "", "Default email for all templates")
	fs.StringVar(&opts.Message, "message", "", "Default message for all templates")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("source directory required")
	}

	imp := snapshot.NewDefault(stdout, stderr)
	created, skipped, err := imp.Init(fs.Arg(0), opts)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Created %d metadata files", created)
	if skipped > 0 {
		fmt.Fprintf(stdout, " (%d skipped, use --overwrite to replace)", skipped)
	}
	fmt.Fprintln(stdout)

	return nil
}

func printPlan(w io.Writer, plan *snapshot.Plan) {
	headerStyle := lipgloss.NewStyle().Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	fmt.Fprintf(w, "%s %d snapshots from %s → %s\n\n",
		headerStyle.Render("Planning:"),
		len(plan.Snapshots),
		plan.SourceDir,
		plan.TargetDir)

	for i, s := range plan.Snapshots {
		fmt.Fprintf(w, "  %2d. %s %s\n",
			i+1,
			s.Name,
			dimStyle.Render(fmt.Sprintf("(%s <%s>)", s.Meta.Author, s.Meta.Email)))
	}

	if len(plan.Warnings) > 0 {
		fmt.Fprintln(w)
		for _, warning := range plan.Warnings {
			fmt.Fprintf(w, "  %s %s\n", warnStyle.Render("⚠"), warning)
		}
	}

	fmt.Fprintln(w)
}

func makeProgressFunc(w io.Writer) func(current, total int, name string) {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	return func(current, total int, name string) {
		pct := float64(current) / float64(total)
		barWidth := 30
		filled := int(pct * float64(barWidth))

		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		fmt.Fprintf(w, "\r\033[K[%s] %d/%d %s", bar, current+1, total, dimStyle.Render(name))
	}
}
