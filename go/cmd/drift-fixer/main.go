package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trevor159/drift-fixer/internal/editor"
	"github.com/trevor159/drift-fixer/internal/finder"
	"github.com/trevor159/drift-fixer/internal/planner"
)

func main() {
	path := flag.String("path", ".", "Path to the Terraform project directory")
	tfBin := flag.String("tf-bin", envOr("DRIFT_FIXER_TF_BIN", "tofu"), "Terraform/OpenTofu binary to use")
	verbose := flag.Bool("verbose", false, "Enable verbose output")
	dryRun := flag.Bool("dry-run", false, "Print what would change without writing files")
	flag.Parse()

	projectDir, err := filepath.Abs(*path)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	fmt.Printf("Starting drift analysis in: %s\n", projectDir)
	fmt.Printf("Using CLI binary: %s\n", *tfBin)

	// Step 1: Run plan and find drifted resources
	fmt.Println("🔍 Running plan to detect drift...")
	drifts, repoIDs, err := planner.Run(projectDir, *tfBin, *verbose)
	if err != nil {
		fatalf("plan: %v", err)
	}

	scriptHook := editor.LoadCommentHook(*verbose)
	if len(drifts) == 0 {
		fmt.Println("✅ No drift detected.")
		return
	}
	fmt.Printf("📊 Found %d resource(s) with drift:\n", len(drifts))
	for _, d := range drifts {
		fmt.Printf("  - %s\n", d.Address)
	}

	// Step 2: Find which files contain drifted resources
	addresses := make([]string, len(drifts))
	for i, d := range drifts {
		addresses[i] = d.Address
	}
	fileMap, err := finder.FindResourceFiles(projectDir, addresses)
	if err != nil {
		fatalf("find files: %v", err)
	}
	if len(fileMap) == 0 {
		fatalf("could not find any .tf files containing the drifted resources")
	}

	// Build address → drift lookup
	driftByAddr := make(map[string]planner.ResourceDrift, len(drifts))
	for _, d := range drifts {
		driftByAddr[d.Address] = d
	}

	// Step 3: Apply fixes
	fmt.Println("📝 Applying fixes...")
	allFixed := true
	for filePath, addrs := range fileMap {
		relPath := relOrAbs(filePath, projectDir)
		for _, addr := range addrs {
			d := driftByAddr[addr]
			if *dryRun {
				if d.Delete {
					fmt.Printf("  [DRY RUN] would remove %s from %s\n", addr, relPath)
				} else {
					fmt.Printf("  [DRY RUN] would update %s in %s\n", addr, relPath)
				}
				continue
			}
			if d.Delete {
				fmt.Printf("\n  Removing %s from %s (deleted from infra)...\n", addr, relPath)
				removed, err := editor.RemoveResource(filePath, d.ResourceType, d.ResourceName)
				if err != nil {
					fmt.Printf("  ❌ %s: %v\n", addr, err)
					allFixed = false
				} else if removed {
					fmt.Printf("  ✅ Removed %s from %s\n", addr, relPath)
				} else {
					fmt.Printf("  ⚠️  %s not found in %s\n", addr, relPath)
				}
				continue
			}
			if *verbose {
				fmt.Printf("\n  Syncing %s in %s\n", addr, relPath)
				fmt.Printf("  Drifted attrs: %s\n", keys(d.DriftedAttrs))
			}
			// Pass 1: write the fix without comments so the script can read
			// the updated file for context in pass 2.
			changed, err := editor.ApplyDrift(filePath, d.ResourceType, d.ResourceName, d.DriftedAttrs, *verbose, nil, repoIDs)
			if err != nil {
				fmt.Printf("  ❌ %s: %v\n", addr, err)
				allFixed = false
				continue
			}
			// Pass 2: apply script-generated comments (file already written).
			if scriptHook != nil {
				_, err = editor.ApplyDrift(filePath, d.ResourceType, d.ResourceName, d.DriftedAttrs, *verbose, scriptHook, repoIDs)
				if err != nil {
					fmt.Printf("  ❌ %s (comment pass): %v\n", addr, err)
				}
			}
			if changed {
				fmt.Printf("  ✅ Fixed %s in %s\n", addr, relPath)
			} else {
				fmt.Printf("  ⚠️  No changes applied for %s\n", addr)
			}
		}
	}

	if *dryRun {
		return
	}

	// Step 4: Validate
	fmt.Println("\n✅ Validating...")
	postDrifts, _, err := planner.Run(projectDir, *tfBin, false)
	if err != nil {
		fatalf("validation plan: %v", err)
	}
	if len(postDrifts) == 0 {
		fmt.Println("✅ Drift fixing completed successfully! No remaining drift.")
	} else if allFixed {
		fmt.Printf("⚠️  %d resource(s) still have drift after fixes — manual review required:\n", len(postDrifts))
		for _, d := range postDrifts {
			fmt.Printf("  - %s: %s\n", d.Address, keys(d.DriftedAttrs))
		}
		os.Exit(1)
	} else {
		fmt.Println("❌ Some resources could not be fixed.")
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}

func relOrAbs(path, base string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

func keys(m map[string]interface{}) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return strings.Join(ks, ", ")
}
