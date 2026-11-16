package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s SourceConfig) approvedRoots(ctx context.Context) ([]string, error) {
	roots := append([]string(nil), s.Roots...)
	if s.Root != "" {
		roots = append([]string{s.Root}, roots...)
	}
	if len(s.RootsCommand) > 0 {
		deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(deadline, s.RootsCommand[0], s.RootsCommand[1:]...)
		// This explicitly approved inventory command supplies paths only; bodies
		// still pass the source snapshot, symlink, privacy and custody boundaries.
		var output limitedInventory
		cmd.Stdout = &output
		cmd.WaitDelay = 250 * time.Millisecond
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("source root inventory unavailable: %w", err)
		}
		var inventory struct {
			Roots []string `json:"roots"`
		}
		if err := json.Unmarshal(output.bytes, &inventory); err != nil {
			return nil, fmt.Errorf("invalid source root inventory")
		}
		roots = append(roots, inventory.Roots...)
	}
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		root = filepath.Clean(root)
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("approved source root must be absolute")
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		if st, err := os.Lstat(root); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("approved source root unavailable or linked: %s", root)
		}
		// A root nested under another would observe the same path twice using
		// different source namespaces. Refuse ambiguity rather than duplicate it.
		for _, other := range out {
			rel, e := filepath.Rel(other, root)
			back, b := filepath.Rel(root, other)
			if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) || b == nil && back != ".." && !strings.HasPrefix(back, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("overlapping approved source roots")
			}
		}
		out = append(out, root)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no approved source roots")
	}
	return out, nil
}

type limitedInventory struct{ bytes []byte }

func (w *limitedInventory) Write(p []byte) (int, error) {
	if len(w.bytes)+len(p) > 1<<20 {
		return 0, fmt.Errorf("source root inventory exceeds 1 MiB")
	}
	w.bytes = append(w.bytes, p...)
	return len(p), nil
}
