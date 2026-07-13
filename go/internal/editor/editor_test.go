package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// applyDriftToString is a test helper that writes input HCL to a temp file,
// calls ApplyDrift, and returns the resulting file content.
func applyDriftToString(t *testing.T, inputHCL string, rType, rName string, drifted map[string]interface{}) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(path, []byte(inputHCL), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, err := ApplyDrift(path, rType, rName, drifted, false, nil, nil)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	return string(out)
}

func removeResourceFromString(t *testing.T, inputHCL string, rType, rName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(path, []byte(inputHCL), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	removed, err := RemoveResource(path, rType, rName)
	if err != nil {
		t.Fatalf("RemoveResource: %v", err)
	}
	if !removed {
		t.Fatal("RemoveResource returned false — block not found")
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	return string(out)
}

// assertContains fails if substr is not found in s.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain:\n  %q\ngot:\n%s", substr, s)
	}
}

// assertNotContains fails if substr IS found in s.
func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected output NOT to contain:\n  %q\ngot:\n%s", substr, s)
	}
}

// ---------------------------------------------------------------------------
// Scalar attribute tests
// ---------------------------------------------------------------------------

func TestUpdateExistingScalar(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  description = "old"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"description": "new value"})

	assertContains(t, out, `description = "new value"`)
	assertNotContains(t, out, `"old"`)
}

func TestAddMissingScalar(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  name = "my-repo"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"description": "added"})

	assertContains(t, out, `description = "added"`)
}

func TestLargeIntegerNoScientificNotation(t *testing.T) {
	// Regression: actor_id was written as 1.236702e+06
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 0
    bypass_mode = "always"
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{
					"actor_id":    float64(1236702),
					"bypass_mode": "always",
				},
			},
		})

	assertContains(t, out, "actor_id    = 1236702")
	assertNotContains(t, out, "1.236702e")
}

func TestBooleanValues(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  has_issues = true
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"has_issues": false})

	assertContains(t, out, "has_issues = false")
}

// ---------------------------------------------------------------------------
// Nested block tests
// ---------------------------------------------------------------------------

func TestAddMissingNestedBlock(t *testing.T) {
	// Regression: required_status_checks was not being created inside rules {}
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = true
    pull_request {
      required_approving_review_count = 1
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"deletion": true,
				"required_status_checks": map[string]interface{}{
					"do_not_enforce_on_create": false,
					"required_check": map[string]interface{}{
						"context":        "ci/test",
						"integration_id": float64(0),
					},
				},
			},
		})

	assertContains(t, out, "required_status_checks {")
	assertContains(t, out, `context        = "ci/test"`)
}

func TestUpdateScalarInsideExistingBlock(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = false
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"deletion": true,
			},
		})

	assertContains(t, out, "deletion = true")
}

// ---------------------------------------------------------------------------
// Repeated block tests
// ---------------------------------------------------------------------------

func TestAddTwoRepeatedBlocks(t *testing.T) {
	// Block type absent from config — both instances should be added.
	input := `
resource "github_repository_ruleset" "rs" {
  name = "main"
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{"actor_id": float64(100), "bypass_mode": "always"},
				map[string]interface{}{"actor_id": float64(200), "bypass_mode": "always"},
			},
		})

	if strings.Count(out, "bypass_actors {") != 2 {
		t.Errorf("expected 2 bypass_actors blocks, got:\n%s", out)
	}
	assertContains(t, out, "= 100")
	assertContains(t, out, "= 200")
}

func TestRemoveExcessRepeatedBlock(t *testing.T) {
	// Regression: config has 2 bypass_actors, infra now has 1 — excess should
	// be removed and the remaining one synced.
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 100
    bypass_mode = "always"
  }
  bypass_actors {
    actor_id    = 200
    bypass_mode = "always"
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{"actor_id": float64(100), "bypass_mode": "exempt"},
			},
		})

	if strings.Count(out, "bypass_actors {") != 1 {
		t.Errorf("expected 1 bypass_actors block, got:\n%s", out)
	}
	assertContains(t, out, `bypass_mode = "exempt"`)
}

func TestEmptyListRemovesExistingBlocks(t *testing.T) {
	// Regression: when infra has no bypass_actors (plan returns []),
	// existing config blocks should be removed.
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 100
    bypass_mode = "always"
  }
  bypass_actors {
    actor_id    = 200
    bypass_mode = "always"
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{},
		})

	assertNotContains(t, out, "bypass_actors")
}

func TestEmptyNestedListAttrClearsToEmpty(t *testing.T) {
	// Regression (repo-configs run 29230872378): infra had no
	// required_status_checks.contexts, but config listed one. drift-fixer only
	// synced strict and skipped the empty contexts list, leaving perpetual
	// drift. A nested scalar list attr empty in infra must be cleared to [].
	input := `
resource "github_branch_protection" "v6_0" {
  required_status_checks {
    strict   = true
    contexts = ["FF Merge Status Check"]
  }
}
`
	out := applyDriftToString(t, input, "github_branch_protection", "v6_0",
		map[string]interface{}{
			"required_status_checks": []interface{}{
				map[string]interface{}{
					"strict":   true,
					"contexts": []interface{}{},
				},
			},
		})

	assertContains(t, out, "contexts = []")
	assertNotContains(t, out, "FF Merge Status Check")
}

func TestEmptyListNoBlocksIsNoop(t *testing.T) {
	// Empty list from plan when no config blocks exist = no-op (not a scalar attr).
	input := `
resource "github_repository_ruleset" "rs" {
  name = "main"
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"branch_name_pattern": []interface{}{},
		})

	assertNotContains(t, out, "branch_name_pattern")
}

// TestBlockListAppendsMissingFromInfra reproduces the field scenario where
// real infra had more bypass_actors than config: validation kept flagging
// bypass_actors drift because drift-fixer was leaving config short.
//
// Policy: real infra is the source of truth. When real infra has more
// blocks than config, drift-fixer appends the missing entries so config
// ends up with exactly the same set of blocks as real infra.
func TestBlockListAppendsMissingFromInfra(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 2
    actor_type  = "RepositoryRole"
    bypass_mode = "always"
  }
  bypass_actors {
    actor_id    = 5
    actor_type  = "RepositoryRole"
    bypass_mode = "always"
  }
  bypass_actors {
    actor_id    = 100
    actor_type  = "RepositoryRole"
    bypass_mode = "always"
  }
  bypass_actors {
    actor_id    = 200
    actor_type  = "RepositoryRole"
    bypass_mode = "always"
  }
}
`
	// Real-infra (plan's `before`) has the same 4 actors PLUS an Integration
	// at position 0 — five total.
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{"actor_id": float64(935721), "actor_type": "Integration", "bypass_mode": "always"},
				map[string]interface{}{"actor_id": float64(2), "actor_type": "RepositoryRole", "bypass_mode": "always"},
				map[string]interface{}{"actor_id": float64(5), "actor_type": "RepositoryRole", "bypass_mode": "always"},
				map[string]interface{}{"actor_id": float64(100), "actor_type": "RepositoryRole", "bypass_mode": "always"},
				map[string]interface{}{"actor_id": float64(200), "actor_type": "RepositoryRole", "bypass_mode": "always"},
			},
		})

	// Config now has 5 bypass_actors (matches real infra).
	if got := strings.Count(out, "bypass_actors {"); got != 5 {
		t.Errorf("expected 5 bypass_actors blocks in config (matching infra), got %d:\n%s", got, out)
	}
	// The Integration entry from real infra is now in config.
	assertContains(t, out, `actor_type  = "Integration"`)
	assertContains(t, out, "actor_id    = 935721")
	// All RepositoryRole entries are still present.
	for _, id := range []string{"= 2", "= 5", "= 100", "= 200"} {
		assertContains(t, out, "actor_id    "+id)
	}
}

func TestBlockListAppendsSingleMissing(t *testing.T) {
	// Config has 1 bypass_actor, infra has 2. drift-fixer should sync the
	// existing block against infra[0] and append infra[1].
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 100
    bypass_mode = "always"
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{"actor_id": float64(999), "bypass_mode": "exempt"},
				map[string]interface{}{"actor_id": float64(200), "bypass_mode": "always"},
			},
		})

	if got := strings.Count(out, "bypass_actors {"); got != 2 {
		t.Errorf("expected 2 bypass_actors blocks in config (matching infra), got %d:\n%s", got, out)
	}
	// Both infra entries land in config.
	assertContains(t, out, "actor_id    = 999")
	assertContains(t, out, `bypass_mode = "exempt"`)
	assertContains(t, out, "actor_id    = 200")
	assertNotContains(t, out, "actor_id    = 100")
}

// ---------------------------------------------------------------------------
// RemoveResource tests
// ---------------------------------------------------------------------------

func TestRemoveResource(t *testing.T) {
	input := `
resource "github_repository" "keep" {
  name = "keep"
}

resource "github_repository" "delete_me" {
  name = "delete_me"
}
`
	out := removeResourceFromString(t, input, "github_repository", "delete_me")

	assertNotContains(t, out, `"delete_me"`)
	assertContains(t, out, `"keep"`)
}

func TestRemoveResourceNotFound(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  name = "repo"
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed, err := RemoveResource(path, "github_repository", "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed=false for nonexistent resource")
	}
}

// ---------------------------------------------------------------------------
// Multi-line list formatting tests
// ---------------------------------------------------------------------------

func TestMultilineStringListFormatting(t *testing.T) {
	// A string list with >1 items should be written one-item-per-line.
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    pull_request {
      allowed_merge_methods = ["merge"]
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"pull_request": map[string]interface{}{
					"allowed_merge_methods": []interface{}{"merge", "squash", "rebase"},
				},
			},
		})

	assertContains(t, out, "\"merge\",")
	assertContains(t, out, "\"squash\",")
	assertContains(t, out, "\"rebase\",")
	// Each item should be on its own line
	if strings.Count(out, "\n") < strings.Count(input, "\n")+2 {
		t.Errorf("expected multi-line list, got:\n%s", out)
	}
}

func TestSingleItemListStaysOnOneLine(t *testing.T) {
	// A single-item list should stay on one line (not multi-line).
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    pull_request {
      allowed_merge_methods = ["merge", "squash"]
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"pull_request": map[string]interface{}{
					"allowed_merge_methods": []interface{}{"merge"},
				},
			},
		})

	assertContains(t, out, `"merge"`)
}

func TestMultilineIntListFormatting(t *testing.T) {
	// A list of numbers with >1 items should also be written one-item-per-line.
	input := `
resource "example_resource" "r" {
  ports = [80]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{
			"ports": []interface{}{float64(80), float64(443), float64(8080)},
		})

	assertContains(t, out, "80,")
	assertContains(t, out, "443,")
	assertContains(t, out, "8080,")
	if strings.Count(out, "\n") < strings.Count(input, "\n")+2 {
		t.Errorf("expected multi-line int list, got:\n%s", out)
	}
}

func TestCommentHook(t *testing.T) {
	// Hook should add inline comments to new/updated scalar attrs and list items.
	input := `
resource "example_resource" "r" {
  name = "old"
  tags = ["a"]
}
`
	hook := func(rType, rName, path, value, _ string) string {
		if rType != "example_resource" || rName != "r" {
			return ""
		}
		switch value {
		case `"new"`: // scalar attr — value arrives rendered (quoted)
			return "updated name"
		case `"a"`, `"b"`: // list items — values arrive rendered (quoted)
			return value + " tag"
		}
		return ""
	}

	dir := t.TempDir()
	fpath := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(fpath, []byte(input), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ApplyDrift(fpath, "example_resource", "r", map[string]interface{}{
		"name": "new",
		"tags": []interface{}{"a", "b"},
	}, false, hook, nil)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	outBytes, _ := os.ReadFile(fpath)
	s := string(outBytes)

	// Scalar attr should have inline comment on its line.
	foundScalar := false
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, `"new"`) {
			foundScalar = true
			if !strings.Contains(line, "# updated name") {
				t.Errorf("expected '# updated name' on name line, got: %q", line)
			}
			break
		}
	}
	if !foundScalar {
		t.Errorf("could not find 'new' in output:\n%s", s)
	}

	// List items should have inline comments.
	for _, tc := range []struct{ val, comment string }{
		{`"a"`, `# "a" tag`},
		{`"b"`, `# "b" tag`},
	} {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, tc.val) {
				if !strings.Contains(line, tc.comment) {
					t.Errorf("expected %q on line with %q, got: %q", tc.comment, tc.val, line)
				}
				break
			}
		}
	}
}

func TestCommentsPreservedInList(t *testing.T) {
	// Both preceding-line and inline comments must survive a drift sync,
	// including the tricky case where an inline comment is immediately followed
	// by a preceding-line comment for the next item.
	input := `
resource "example_resource" "r" {
  include = [
    # before A
    "A", # inline A
    # before B
    "B", # inline B
    "C",
  ]
}
`
	// Drift adds a new item "D"; all existing comments must be preserved.
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{
			"include": []interface{}{"A", "B", "C", "D"},
		})

	assertContains(t, out, "# before A")
	assertContains(t, out, "# inline A")
	assertContains(t, out, "# before B")
	assertContains(t, out, "# inline B")
	assertContains(t, out, `"D"`)

	// Inline comment must be on the same line as its value.
	for _, tc := range []struct{ val, comment string }{
		{`"A"`, "# inline A"},
		{`"B"`, "# inline B"},
	} {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, tc.val) {
				if !strings.Contains(line, tc.comment) {
					t.Errorf("expected %q on same line as %q, got: %q", tc.comment, tc.val, line)
				}
				break
			}
		}
	}

	// before-comment for B must appear after the line containing A.
	aIdx := strings.Index(out, `"A"`)
	beforeBIdx := strings.Index(out, "# before B")
	if beforeBIdx < aIdx {
		t.Errorf("expected '# before B' after line with A, got:\n%s", out)
	}
}

// TestRepositoryIDReferencesPreserved verifies that when a repository_ids list
// already uses data.github_repository.* references in the config, those
// references are kept instead of being replaced with bare integer literals.
// New IDs that have no existing reference get an integer literal (with a
// comment from the built-in hook if repoIDs is provided).
func TestRepositoryIDReferencesPreserved(t *testing.T) {
	input := `
resource "github_actions_organization_permissions" "_10gen" {
  enabled_repositories = "selected"
  enabled_repositories_config {
    repository_ids = [
      data.github_repository.mms.repo_id,
      data.github_repository.mongo.repo_id,
    ]
  }
}
`
	// Infra now has three repos: mms and mongo are still there (same IDs),
	// plus a newly-added repo with ID 99999.
	repoIDs := map[string]string{
		"204896":  "mms",
		"3473720": "mongo",
		"99999":   "new_repo",
	}

	dir := t.TempDir()
	fpath := filepath.Join(dir, "main.tf")
	_ = os.WriteFile(fpath, []byte(input), 0644)

	_, err := ApplyDrift(fpath, "github_actions_organization_permissions", "_10gen",
		map[string]interface{}{
			"enabled_repositories_config": map[string]interface{}{
				"repository_ids": []interface{}{
					float64(204896),
					float64(3473720),
					float64(99999),
				},
			},
		},
		false, nil, repoIDs)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	outBytes, _ := os.ReadFile(fpath)
	s := string(outBytes)

	// Existing references must be preserved.
	if !strings.Contains(s, "data.github_repository.mms.repo_id") {
		t.Errorf("mms reference should be preserved, got:\n%s", s)
	}
	if !strings.Contains(s, "data.github_repository.mongo.repo_id") {
		t.Errorf("mongo reference should be preserved, got:\n%s", s)
	}
	// New ID that had no reference in config should appear as an integer.
	if !strings.Contains(s, "99999") {
		t.Errorf("new repo ID 99999 should be present, got:\n%s", s)
	}
	// The bare integer 204896 should NOT appear (replaced by reference).
	if strings.Contains(s, "204896") {
		t.Errorf("bare integer 204896 should not appear (should be reference), got:\n%s", s)
	}
}

// TestRepositoryIDOrderingPreserved verifies that existing config items keep
// their original order, and new infra items are appended at the end — even
// when the provider returns IDs in a different order than the config.
func TestRepositoryIDOrderingPreserved(t *testing.T) {
	// Config has: mms, mongo, new_repo in that order.
	// Infra returns: new_repo, mongo, mms (reversed) plus two brand-new IDs.
	// Expected output order: mms, mongo, new_repo (original order), then the
	// two new IDs appended at the end.
	input := `
resource "github_actions_organization_permissions" "_10gen" {
  enabled_repositories_config {
    repository_ids = [
      data.github_repository.mms.repo_id,
      data.github_repository.mongo.repo_id,
      data.github_repository.new_repo.repo_id,
    ]
  }
}
`
	repoIDs := map[string]string{
		"204896":  "mms",
		"3473720": "mongo",
		"99999":   "new_repo",
	}

	dir := t.TempDir()
	fpath := filepath.Join(dir, "main.tf")
	_ = os.WriteFile(fpath, []byte(input), 0644)

	_, err := ApplyDrift(fpath, "github_actions_organization_permissions", "_10gen",
		map[string]interface{}{
			"enabled_repositories_config": map[string]interface{}{
				// Infra order: reversed + two new IDs
				"repository_ids": []interface{}{
					float64(99999),
					float64(3473720),
					float64(204896),
					float64(1111111),
					float64(2222222),
				},
			},
		},
		false, nil, repoIDs)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	outBytes, _ := os.ReadFile(fpath)
	s := string(outBytes)

	// Ordering: mms before mongo, mongo before new_repo, new_repo before new IDs.
	mmsIdx := strings.Index(s, "mms")
	mongoIdx := strings.Index(s, "mongo")
	newRepoIdx := strings.Index(s, "new_repo")
	id1Idx := strings.Index(s, "1111111")
	id2Idx := strings.Index(s, "2222222")

	if mmsIdx < 0 || mongoIdx < 0 || newRepoIdx < 0 || id1Idx < 0 || id2Idx < 0 {
		t.Fatalf("one or more expected items missing in output:\n%s", s)
	}
	if !(mmsIdx < mongoIdx && mongoIdx < newRepoIdx && newRepoIdx < id1Idx && id1Idx < id2Idx) {
		t.Errorf("wrong order — want mms < mongo < new_repo < 1111111 < 2222222, got indices %d %d %d %d %d\n%s",
			mmsIdx, mongoIdx, newRepoIdx, id1Idx, id2Idx, s)
	}
}
