package reviewartifact

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// queueDirListing remembers each review directory's listing for one Queue
// call. Without it every queued item re-read its own directory twice (once for
// sidecar extras, once for its work-binding glob), so an area with n files cost
// O(n^2) directory entries. One listing per directory per call is also a more
// consistent snapshot: every item in a directory sees the same files.
//
// A nil listing is the reference path: it reads the directory every time, as
// the queue did before, so tests can compare both on the same home.
type queueDirListing struct {
	dirs map[string]queueDirEntries
}

type queueDirEntries struct {
	entries []os.DirEntry
	err     error
}

func newQueueDirListing() *queueDirListing {
	return &queueDirListing{dirs: map[string]queueDirEntries{}}
}

// readDir is os.ReadDir, answered once per directory per listing. The error is
// remembered too, so a failing directory fails every caller the same way.
func (l *queueDirListing) readDir(dir string) ([]os.DirEntry, error) {
	if l == nil {
		return os.ReadDir(dir)
	}
	if cached, ok := l.dirs[dir]; ok {
		return cached.entries, cached.err
	}
	entries, err := os.ReadDir(dir)
	l.dirs[dir] = queueDirEntries{entries: entries, err: err}
	return entries, err
}

// glob is filepath.Glob answered from the remembered listing when it can be
// answered exactly: the pattern is well formed, only its last element has
// meta characters, and the directory listed without error. It then matches
// the same names with the same filepath.Match in the same sorted order Glob
// uses. Every other case is delegated to filepath.Glob itself.
func (l *queueDirListing) glob(pattern string) ([]string, error) {
	if l == nil {
		return filepath.Glob(pattern)
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, err
	}
	dir, file := filepath.Split(pattern)
	dir = cleanQueueGlobDir(dir)
	if runtime.GOOS == "windows" || !queueGlobHasMeta(pattern) || queueGlobHasMeta(dir) {
		return filepath.Glob(pattern)
	}
	entries, err := l.readDir(dir)
	if err != nil {
		return filepath.Glob(pattern)
	}
	var matches []string
	for _, entry := range entries {
		matched, err := filepath.Match(file, entry.Name())
		if err != nil {
			return matches, err
		}
		if matched {
			matches = append(matches, filepath.Join(dir, entry.Name()))
		}
	}
	return matches, nil
}

// cleanQueueGlobDir mirrors path/filepath's cleanGlobPath on non-Windows hosts.
func cleanQueueGlobDir(dir string) string {
	switch dir {
	case "":
		return "."
	case string(filepath.Separator):
		return dir
	default:
		return dir[:len(dir)-1]
	}
}

// queueGlobHasMeta mirrors path/filepath's hasMeta on non-Windows hosts.
func queueGlobHasMeta(path string) bool {
	return strings.ContainsAny(path, `*?[\`)
}
