package snapshot

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (imp *Importer) checkOverlap(source, target string) error {
	for _, pair := range [][2]string{{source, target}, {target, source}} {
		root, path := pair[0], pair[1]
		info, err := imp.FS.Stat(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue // A missing directory cannot contain the other directory.
		}
		if err != nil {
			return err
		}

		// Check both the named ancestors and the physical ancestors. Force
		// can remove a link in the named path even when it points elsewhere.
		named, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		resolved, err := imp.resolveExistingAncestor(path)
		if err != nil {
			return err
		}
		for _, ancestor := range []string{named, resolved} {
			for {
				parentInfo, err := imp.FS.Stat(ancestor)
				if err == nil && (ancestor == root || os.SameFile(info, parentInfo)) {
					return fmt.Errorf("source and target directories overlap: %s and %s", source, target)
				}
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				parent := filepath.Dir(ancestor)
				if parent == ancestor {
					break
				}
				ancestor = parent
			}
		}
	}
	return nil
}

// resolveExistingAncestor follows symlinks before interpreting "..". When the
// target does not exist yet, its nearest existing parent determines its location.
func (imp *Importer) resolveExistingAncestor(path string) (string, error) {
	for {
		resolved, err := imp.FS.EvalSymlinks(path)
		if err == nil {
			return filepath.Abs(resolved)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		// Split the original path without cleaning away a symlink before "..".
		dir, _ := filepath.Split(strings.TrimRight(filepath.ToSlash(path), "/"))
		if dir == "" {
			dir = "."
		}
		if dir == path {
			return "", err
		}
		path = dir
	}
}
