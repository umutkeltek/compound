package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/panel"
)

func cmdPanel(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound panel <import-lenses <manifest>|lenses --target-kind K [--json]>")
		os.Exit(2)
	}
	switch args[0] {
	case "import-lenses":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound panel import-lenses <manifest>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		res, err := panel.ImportLensManifest(homePath, args[1], time.Now())
		die(err)
		fmt.Printf("# panel lens import %s\n", shortHash(res.ManifestSHA256))
		fmt.Printf("- imported: %d\n", res.Imported)
		fmt.Printf("- nodes_changed: %d\n", res.NodesChanged)
		fmt.Printf("- prompts_changed: %d\n", res.PromptsChanged)
		fmt.Printf("- authority_appended: %t\n", res.AuthorityAppended)
	case "lenses":
		targetKind, jsonOut := "", false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--target-kind":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: compound panel lenses --target-kind K [--json]")
					os.Exit(2)
				}
				targetKind = args[i+1]
				i++
			case "--json":
				jsonOut = true
			default:
				fmt.Fprintf(os.Stderr, "unknown panel lenses arg %q\n", args[i])
				os.Exit(2)
			}
		}
		if strings.TrimSpace(targetKind) == "" {
			fmt.Fprintln(os.Stderr, "usage: compound panel lenses --target-kind K [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		selection, err := panel.SelectLenses(homePath, targetKind)
		die(err)
		if jsonOut {
			printJSON(selection)
			return
		}
		fmt.Printf("# panel lenses target_kind=%s\n", targetKind)
		fmt.Printf("included (%d):\n", len(selection.Included))
		for _, lens := range selection.Included {
			fmt.Printf("- %s weight=%.2f prompt_sha256=%s\n", lens.NodeID, lens.Weight, lens.PromptSHA256)
		}
		fmt.Printf("excluded (%d):\n", len(selection.Excluded))
		for _, excluded := range selection.Excluded {
			fmt.Printf("- %s reason=%s\n", excluded.Ref, excluded.Reason)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound panel <import-lenses <manifest>|lenses --target-kind K [--json]>")
		os.Exit(2)
	}
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
