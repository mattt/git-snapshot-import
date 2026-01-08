# git-snapshot-import

Convert timestamped directory snapshots into Git commits.

## Installation

```
go install github.com/mattt/git-snapshot-import@latest
```

Once installed, you can also invoke it as a Git subcommand:

```
git snapshot-import <source-dir> <target-dir>
```

## Usage

```
git-snapshot-import <source-dir> <target-dir> [flags]
```

Given a directory containing timestamped subdirectories:

```
constitution-drafts/
    1787-09-17/
        constitution.txt
    1788-06-21/
        constitution.txt
    1791-12-15/
        constitution.txt
        bill-of-rights.txt
```

Create a Git repository with one commit per snapshot:

```
git-snapshot-import ./constitution-drafts ./constitution-git
```

This creates `./constitution-git/` as a new Git repository
where each commit corresponds to a snapshot, dated accordingly.

You can then view the commit history:

```
cd ./constitution-git
git log --oneline --date=short --format="%h %ad %s"
```

This will show each snapshot as a separate commit,
with dates matching the original snapshot timestamps.

You can view the details of a specific commit:

```
git show HEAD
```

Example output:

```
commit abc123def456...
Author: James Madison <jmadison@example.com>
Date:   Mon Sep 17 00:00:00 1787 +0000

    Initial draft of the Constitution

diff --git a/constitution.txt b/constitution.txt
new file mode 100644
index 0000000..abc1234
--- /dev/null
+++ b/constitution.txt
@@ -0,0 +1,10 @@
+We the People of the United States...
```

### Commands

```
git-snapshot-import <source> <target>   Import snapshots into new Git repo
git-snapshot-import init <source>       Generate metadata templates
git-snapshot-import --continue <target> Resume interrupted import
git-snapshot-import --abort <target>    Cancel interrupted import
```

### Flags

```
-n, --dry-run        Show what would be done without making changes
-f, --force          Overwrite target directory if it exists
-b, --branch         Branch name to create (default: main)
--default-author     Default author name
--default-email      Default email address
```

## Timestamp Formats

Directory names must be valid ISO 8601 timestamps:

```
1787-09-17                 Date only (midnight UTC)
1787-09-17T16:00:00        With time
1787-09-17T16:00:00Z       With UTC marker
1787-09-17T11:00:00-05:00  With timezone offset
```

## Metadata

Each snapshot directory may contain a `.git-snapshot.json` file
to specify commit metadata:

```json
{
  "author": "James Madison",
  "email": "jmadison@example.com",
  "message": "Initial draft of the Constitution"
}
```

To generate template metadata files for all snapshots:

```
git-snapshot-import init ./constitution-drafts --author "James Madison"
```

## License

MIT
