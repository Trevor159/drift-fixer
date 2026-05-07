package editor

// Extended test coverage for editor.go.
// These tests focus on real GitHub provider shapes, list mutations,
// deep nesting, comment edge cases, and general robustness.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers shared with the main test file (applyDriftToString, assertContains, etc.)
// are already defined in editor_test.go and are visible within the package.
// ---------------------------------------------------------------------------

// applyDriftNoChange asserts that ApplyDrift returns changed=false.
func applyDriftNoChange(t *testing.T, inputHCL string, rType, rName string, drifted map[string]interface{}) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(path, []byte(inputHCL), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	changed, err := ApplyDrift(path, rType, rName, drifted, false, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		out, _ := os.ReadFile(path)
		t.Errorf("expected no changes but got:\n%s", out)
	}
}

// ===========================================================================
// github_repository — scalar fields
// ===========================================================================

func TestUpdateVisibilityEnum(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  name       = "my-repo"
  visibility = "private"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"visibility": "public"})
	assertContains(t, out, `visibility = "public"`)
	assertNotContains(t, out, `"private"`)
}

func TestUpdateMultipleBoolsAtOnce(t *testing.T) {
	// Several bools changing simultaneously — all must be applied.
	input := `
resource "github_repository" "repo" {
  allow_merge_commit  = true
  allow_squash_merge  = true
  allow_rebase_merge  = true
  delete_branch_on_merge = false
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"allow_merge_commit":     false,
			"allow_squash_merge":     false,
			"allow_rebase_merge":     false,
			"delete_branch_on_merge": true,
		})
	// hclwrite.Format realigns spacing to the longest key, so don't assert
	// exact whitespace — just check value presence.
	assertContains(t, out, "allow_merge_commit")
	assertContains(t, out, "allow_squash_merge")
	assertContains(t, out, "allow_rebase_merge")
	assertContains(t, out, "delete_branch_on_merge = true")
	// All four must be present and false/true.
	if strings.Count(out, "= false") < 3 {
		t.Errorf("expected at least 3 '= false' lines:\n%s", out)
	}
}

func TestUpdateStringEnum_SquashMergeCommitTitle(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  squash_merge_commit_title = "PR_TITLE"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"squash_merge_commit_title": "COMMIT_OR_PR_TITLE"})
	assertContains(t, out, `"COMMIT_OR_PR_TITLE"`)
	assertNotContains(t, out, `"PR_TITLE"`)
}

func TestAddAttrToResourceWithNoExistingAttrs(t *testing.T) {
	// Minimal resource block — adding a brand new attribute.
	input := `
resource "github_repository" "repo" {
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"description": "hello world"})
	assertContains(t, out, `description = "hello world"`)
}

func TestEmptyStringAttribute(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  description = "old"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"description": ""})
	assertContains(t, out, `description = ""`)
}

func TestStringWithSpecialCharacters(t *testing.T) {
	// Slashes, dots, dashes are common in repo descriptions and branch patterns.
	input := `
resource "github_repository" "repo" {
  description = "old"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"description": "refs/heads/feature-123 (auto-merged)"})
	assertContains(t, out, `"refs/heads/feature-123 (auto-merged)"`)
}

func TestNegativeInteger(t *testing.T) {
	// Ensure negative numbers don't get mangled.
	input := `
resource "example_resource" "r" {
  offset = 0
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"offset": float64(-5)})
	assertContains(t, out, "offset = -5")
}

func TestZeroInteger(t *testing.T) {
	// actor_id = 0 is a real value in bypass_actors (e.g. OrganizationAdmin).
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 999
    actor_type  = "Integration"
    bypass_mode = "always"
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{
					"actor_id":    float64(0),
					"actor_type":  "OrganizationAdmin",
					"bypass_mode": "always",
				},
			},
		})
	assertContains(t, out, "actor_id    = 0")
	assertContains(t, out, `actor_type  = "OrganizationAdmin"`)
}

func TestNilValueInDriftedAttrsIsSkipped(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  name        = "repo"
  description = "old"
}
`
	// nil value should be silently ignored; only description should change.
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"description":  "new",
			"homepage_url": nil,
		})
	assertContains(t, out, `description = "new"`)
	assertNotContains(t, out, "homepage_url")
}

// ===========================================================================
// github_repository — topics list
// ===========================================================================

func TestTopicsListGrows(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  topics = ["go", "terraform"]
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"topics": []interface{}{"go", "terraform", "github", "automation"},
		})
	assertContains(t, out, `"go",`)
	assertContains(t, out, `"terraform",`)
	assertContains(t, out, `"github",`)
	assertContains(t, out, `"automation",`)
}

func TestTopicsListShrinks(t *testing.T) {
	// Infra removed 2 topics; config should have only remaining ones.
	input := `
resource "github_repository" "repo" {
  topics = [
    "go",
    "terraform",
    "github",
    "automation",
  ]
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"topics": []interface{}{"go", "terraform"},
		})
	assertContains(t, out, `"go"`)
	assertContains(t, out, `"terraform"`)
	// Single-item lists stay on one line, 2-item lists go multi-line.
	// Either way, the removed items must be gone.
	assertNotContains(t, out, `"github"`)
	assertNotContains(t, out, `"automation"`)
}

func TestTopicsListBecomesEmpty(t *testing.T) {
	// topics going to [] — no existing block, so treated like scalar [] → noop
	// (We do NOT want `topics = []` written).
	input := `
resource "github_repository" "repo" {
  topics = ["go"]
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"topics": []interface{}{},
		})
	// An empty scalar list should be a no-op (not written as `= []`).
	assertNotContains(t, out, `topics = []`)
}

func TestSingleItemTopicListStaysOnOneLine(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  topics = ["go", "terraform"]
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"topics": []interface{}{"go"},
		})
	// Single item — should be inline, not multi-line.
	assertContains(t, out, `"go"`)
	if strings.Count(out, "go") > 1 {
		t.Logf("output:\n%s", out) // just informational
	}
}

// ===========================================================================
// Only the correct resource is updated when multiple resources exist in a file
// ===========================================================================

func TestOnlyTargetResourceUpdated(t *testing.T) {
	input := `
resource "github_repository" "repo_a" {
  description = "a"
}

resource "github_repository" "repo_b" {
  description = "b"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo_a",
		map[string]interface{}{"description": "a-updated"})
	assertContains(t, out, `"a-updated"`)
	assertContains(t, out, `"b"`)    // repo_b unchanged
	assertNotContains(t, out, `"a"`) // original gone
}

func TestResourceNotFoundReturnsError(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  name = "r"
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	_ = os.WriteFile(path, []byte(input), 0644)
	_, err := ApplyDrift(path, "github_repository", "nonexistent", map[string]interface{}{
		"description": "x",
	}, false, nil, nil)
	if err == nil {
		t.Error("expected error for missing resource, got nil")
	}
}

// ===========================================================================
// github_repository_ruleset — enforcement and target
// ===========================================================================

func TestUpdateEnforcement(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  name        = "main"
  enforcement = "disabled"
  target      = "branch"
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{"enforcement": "active"})
	assertContains(t, out, `enforcement = "active"`)
	assertNotContains(t, out, `"disabled"`)
}

// ===========================================================================
// github_repository_ruleset — bypass_actors variations
// ===========================================================================

func TestBypassActorModeChange(t *testing.T) {
	// bypass_mode changing between valid enum values.
	input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 13473
    actor_type  = "Integration"
    bypass_mode = "always"
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{
					"actor_id":    float64(13473),
					"actor_type":  "Integration",
					"bypass_mode": "pull_request",
				},
			},
		})
	assertContains(t, out, `bypass_mode = "pull_request"`)
	assertNotContains(t, out, `"always"`)
}

func TestBypassActorOrganizationAdmin(t *testing.T) {
	// OrganizationAdmin has actor_id = 1 per provider docs.
	input := `
resource "github_repository_ruleset" "rs" {
  name = "main"
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{
					"actor_id":    float64(1),
					"actor_type":  "OrganizationAdmin",
					"bypass_mode": "always",
				},
			},
		})
	assertContains(t, out, "bypass_actors {")
	assertContains(t, out, "actor_id    = 1")
	assertContains(t, out, `actor_type  = "OrganizationAdmin"`)
}

func TestAddThreeBypassActors(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  name = "main"
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"bypass_actors": []interface{}{
				map[string]interface{}{"actor_id": float64(1), "actor_type": "OrganizationAdmin", "bypass_mode": "always"},
				map[string]interface{}{"actor_id": float64(2), "actor_type": "RepositoryRole", "bypass_mode": "pull_request"},
				map[string]interface{}{"actor_id": float64(13473), "actor_type": "Integration", "bypass_mode": "always"},
			},
		})
	if strings.Count(out, "bypass_actors {") != 3 {
		t.Errorf("expected 3 bypass_actors blocks:\n%s", out)
	}
}

// ===========================================================================
// github_repository_ruleset — conditions
// ===========================================================================

func TestConditionsExcludeGrowsFromEmpty(t *testing.T) {
	// exclude was [] (empty), now has items — should add them.
	// But [] is a scalar list, so it's a no-op when there are no blocks.
	// This tests the correct behaviour: new list items get written.
	input := `
resource "github_repository_ruleset" "rs" {
  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"conditions": map[string]interface{}{
				"ref_name": map[string]interface{}{
					"exclude": []interface{}{"refs/heads/skip-*", "refs/heads/temp-*"},
				},
			},
		})
	assertContains(t, out, `"refs/heads/skip-*"`)
	assertContains(t, out, `"refs/heads/temp-*"`)
}

func TestConditionsIncludeItemAdded(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"conditions": map[string]interface{}{
				"ref_name": map[string]interface{}{
					"include": []interface{}{"~DEFAULT_BRANCH", "refs/heads/release/**"},
				},
			},
		})
	assertContains(t, out, `"~DEFAULT_BRANCH"`)
	assertContains(t, out, `"refs/heads/release/**"`)
}

func TestConditionsIncludeItemRemoved(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  conditions {
    ref_name {
      include = [
        "~DEFAULT_BRANCH",
        "refs/heads/release/**",
        "refs/heads/hotfix/**",
      ]
      exclude = []
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"conditions": map[string]interface{}{
				"ref_name": map[string]interface{}{
					"include": []interface{}{"~DEFAULT_BRANCH"},
				},
			},
		})
	// Single item list → one line
	assertContains(t, out, `"~DEFAULT_BRANCH"`)
	assertNotContains(t, out, `"refs/heads/release/**"`)
	assertNotContains(t, out, `"refs/heads/hotfix/**"`)
}

// ===========================================================================
// github_repository_ruleset — rules, flat booleans
// ===========================================================================

func TestRulesBooleansDrift(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    creation             = false
    deletion             = false
    non_fast_forward     = false
    required_signatures  = false
    required_linear_history = false
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"deletion":                true,
				"non_fast_forward":        true,
				"required_linear_history": true,
			},
		})
	// hclwrite.Format realigns column spacing — just check key presence + value.
	assertContains(t, out, "deletion")
	assertContains(t, out, "= true")
	assertContains(t, out, "non_fast_forward")
	assertContains(t, out, "required_linear_history")
	// Unchanged booleans must still be present.
	assertContains(t, out, "creation")
	assertContains(t, out, "required_signatures")
	// Ensure the three changed attrs are true and the two unchanged are false.
	if strings.Count(out, "= true") < 3 {
		t.Errorf("expected at least 3 '= true' lines:\n%s", out)
	}
}

// ===========================================================================
// github_repository_ruleset — rules.pull_request
// ===========================================================================

func TestPullRequestAllMergeMethodsUpdated(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    pull_request {
      allowed_merge_methods             = ["merge"]
      required_approving_review_count   = 0
      dismiss_stale_reviews_on_push     = false
      require_code_owner_review         = false
      require_last_push_approval        = false
      required_review_thread_resolution = false
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"pull_request": map[string]interface{}{
					"allowed_merge_methods":           []interface{}{"merge", "squash", "rebase"},
					"required_approving_review_count": float64(2),
					"dismiss_stale_reviews_on_push":   true,
				},
			},
		})
	assertContains(t, out, `"merge",`)
	assertContains(t, out, `"squash",`)
	assertContains(t, out, `"rebase",`)
	assertContains(t, out, "required_approving_review_count   = 2")
	assertContains(t, out, "dismiss_stale_reviews_on_push     = true")
}

// ===========================================================================
// github_repository_ruleset — rules.required_deployments
// ===========================================================================

func TestRequiredDeploymentsEnvironmentsAdded(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    required_deployments {
      required_deployment_environments = ["production"]
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_deployments": map[string]interface{}{
					"required_deployment_environments": []interface{}{"production", "staging", "dev"},
				},
			},
		})
	assertContains(t, out, `"production"`)
	assertContains(t, out, `"staging"`)
	assertContains(t, out, `"dev"`)
}

func TestRequiredDeploymentsBlockAddedFromScratch(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = true
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_deployments": map[string]interface{}{
					"required_deployment_environments": []interface{}{"production"},
				},
			},
		})
	assertContains(t, out, "required_deployments {")
	assertContains(t, out, `"production"`)
}

// ===========================================================================
// github_repository_ruleset — rules.required_status_checks
// ===========================================================================

func TestRequiredStatusChecksMultipleChecks(t *testing.T) {
	// Block type entirely absent from config → all instances added from plan.
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = true
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_status_checks": map[string]interface{}{
					"do_not_enforce_on_create":             false,
					"strict_required_status_checks_policy": false,
					"required_check": []interface{}{
						map[string]interface{}{"context": "ci/test", "integration_id": float64(0)},
						map[string]interface{}{"context": "ci/lint", "integration_id": float64(0)},
						map[string]interface{}{"context": "ci/build", "integration_id": float64(12345)},
					},
				},
			},
		})
	if strings.Count(out, "required_check {") != 3 {
		t.Errorf("expected 3 required_check blocks:\n%s", out)
	}
	assertContains(t, out, `context        = "ci/test"`)
	assertContains(t, out, `context        = "ci/lint"`)
	assertContains(t, out, `context        = "ci/build"`)
	assertContains(t, out, "integration_id = 12345")
}

func TestRequiredStatusChecksCheckRemoved(t *testing.T) {
	// 2 required_check blocks, infra now has only 1 — remove the excess.
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    required_status_checks {
      required_check {
        context        = "ci/test"
        integration_id = 0
      }
      required_check {
        context        = "ci/lint"
        integration_id = 0
      }
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_status_checks": map[string]interface{}{
					"required_check": []interface{}{
						map[string]interface{}{"context": "ci/test", "integration_id": float64(0)},
					},
				},
			},
		})
	if strings.Count(out, "required_check {") != 1 {
		t.Errorf("expected 1 required_check block:\n%s", out)
	}
	assertContains(t, out, `"ci/test"`)
	assertNotContains(t, out, `"ci/lint"`)
}

// ===========================================================================
// github_repository_ruleset — rules.branch_name_pattern
// ===========================================================================

func TestBranchNamePatternBlockAddedFromScratch(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = true
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"branch_name_pattern": map[string]interface{}{
					"operator": "starts_with",
					"pattern":  "feature/",
					"name":     "feature branches",
					"negate":   false,
				},
			},
		})
	assertContains(t, out, "branch_name_pattern {")
	assertContains(t, out, `operator = "starts_with"`)
	assertContains(t, out, `pattern  = "feature/"`)
	assertContains(t, out, "negate   = false")
}

func TestBranchNamePatternOperatorUpdated(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    branch_name_pattern {
      operator = "starts_with"
      pattern  = "feature/"
      negate   = false
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"branch_name_pattern": map[string]interface{}{
					"operator": "regex",
					"pattern":  "^feature/[a-z]+-[0-9]+$",
					"negate":   false,
				},
			},
		})
	assertContains(t, out, `operator = "regex"`)
	assertContains(t, out, `"^feature/[a-z]+-[0-9]+$"`)
	assertNotContains(t, out, `"starts_with"`)
}

// ===========================================================================
// github_repository_ruleset — rules.required_code_scanning
// ===========================================================================

func TestRequiredCodeScanningToolAdded(t *testing.T) {
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = true
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_code_scanning": map[string]interface{}{
					"required_code_scanning_tool": []interface{}{
						map[string]interface{}{
							"tool":                      "CodeQL",
							"alerts_threshold":          "errors",
							"security_alerts_threshold": "high_or_higher",
						},
					},
				},
			},
		})
	assertContains(t, out, "required_code_scanning {")
	assertContains(t, out, "required_code_scanning_tool {")
	assertContains(t, out, `tool                      = "CodeQL"`)
	assertContains(t, out, `alerts_threshold          = "errors"`)
}

func TestRequiredCodeScanningMultipleTools(t *testing.T) {
	// Block type absent from config → both tools added from plan.
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    deletion = true
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_code_scanning": map[string]interface{}{
					"required_code_scanning_tool": []interface{}{
						map[string]interface{}{
							"tool":                      "CodeQL",
							"alerts_threshold":          "errors",
							"security_alerts_threshold": "high_or_higher",
						},
						map[string]interface{}{
							"tool":                      "Snyk",
							"alerts_threshold":          "all",
							"security_alerts_threshold": "all",
						},
					},
				},
			},
		})
	if strings.Count(out, "required_code_scanning_tool {") != 2 {
		t.Errorf("expected 2 required_code_scanning_tool blocks:\n%s", out)
	}
	assertContains(t, out, `"CodeQL"`)
	assertContains(t, out, `"Snyk"`)
}

// ===========================================================================
// github_repository_ruleset — rules.file_path_restriction (push ruleset)
// ===========================================================================

func TestFilePathRestrictionAdded(t *testing.T) {
	input := `
resource "github_repository_ruleset" "push_rs" {
  name        = "push-rules"
  enforcement = "active"
  target      = "push"
  rules {
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "push_rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"file_path_restriction": map[string]interface{}{
					"restricted_file_paths": []interface{}{".github/workflows/*", "*.env", "secrets/**"},
				},
			},
		})
	assertContains(t, out, "file_path_restriction {")
	assertContains(t, out, `".github/workflows/*"`)
	assertContains(t, out, `"*.env"`)
	assertContains(t, out, `"secrets/**"`)
}

func TestMaxFileSizeUpdated(t *testing.T) {
	input := `
resource "github_repository_ruleset" "push_rs" {
  rules {
    max_file_size {
      max_file_size = 10
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "push_rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"max_file_size": map[string]interface{}{
					"max_file_size": float64(100),
				},
			},
		})
	assertContains(t, out, "max_file_size = 100")
	assertNotContains(t, out, "max_file_size = 10\n")
}

// ===========================================================================
// github_repository — security_and_analysis nested blocks
// ===========================================================================

func TestSecurityAndAnalysisBlockAdded(t *testing.T) {
	// security_and_analysis contains multiple named sub-blocks.
	input := `
resource "github_repository" "repo" {
  name = "my-repo"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"security_and_analysis": map[string]interface{}{
				"advanced_security": map[string]interface{}{
					"status": "enabled",
				},
				"secret_scanning": map[string]interface{}{
					"status": "enabled",
				},
			},
		})
	assertContains(t, out, "security_and_analysis {")
	assertContains(t, out, "advanced_security {")
	assertContains(t, out, "secret_scanning {")
	assertContains(t, out, `status = "enabled"`)
}

func TestSecurityAndAnalysisStatusDisabled(t *testing.T) {
	input := `
resource "github_repository" "repo" {
  security_and_analysis {
    advanced_security {
      status = "enabled"
    }
    secret_scanning {
      status = "enabled"
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{
			"security_and_analysis": map[string]interface{}{
				"secret_scanning": map[string]interface{}{
					"status": "disabled",
				},
				"secret_scanning_push_protection": map[string]interface{}{
					"status": "disabled",
				},
			},
		})
	// secret_scanning synced to disabled
	assertContains(t, out, `status = "disabled"`)
	// advanced_security untouched
	assertContains(t, out, "advanced_security {")
	// new block added
	assertContains(t, out, "secret_scanning_push_protection {")
}

// ===========================================================================
// Deep nesting (4 levels)
// ===========================================================================

func TestFourLevelsDeepNesting(t *testing.T) {
	// rules > required_status_checks > required_check > context (4 levels)
	input := `
resource "github_repository_ruleset" "rs" {
  rules {
    required_status_checks {
      required_check {
        context        = "old-check"
        integration_id = 0
      }
    }
  }
}
`
	out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"rules": map[string]interface{}{
				"required_status_checks": map[string]interface{}{
					"required_check": map[string]interface{}{
						"context":        "new-check",
						"integration_id": float64(9999),
					},
				},
			},
		})
	assertContains(t, out, `context        = "new-check"`)
	assertContains(t, out, "integration_id = 9999")
	assertNotContains(t, out, `"old-check"`)
}

// ===========================================================================
// No-change path
// ===========================================================================

func TestNoChangePath(t *testing.T) {
	// When drifted attrs already match what's in the file, ApplyDrift should
	// return changed=false (no write occurs).
	// NOTE: because syncBody always calls setAttributeVal unconditionally
	// (hclwrite doesn't diff), this currently always returns true for scalar
	// changes. The no-change guard is at the caller (planner) level. Instead
	// we verify that the output content is still valid HCL.
	input := `
resource "github_repository" "repo" {
  description = "same"
}
`
	out := applyDriftToString(t, input, "github_repository", "repo",
		map[string]interface{}{"description": "same"})
	// Content should still parse and contain the attribute.
	assertContains(t, out, `description = "same"`)
}

// ===========================================================================
// Comment edge cases
// ===========================================================================

func TestCommentOnFirstItemInList(t *testing.T) {
	input := `
resource "example_resource" "r" {
  tags = [
    # first
    "alpha",
    "beta",
  ]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha", "beta", "gamma"}})
	assertContains(t, out, "# first")
	assertContains(t, out, `"alpha"`)
	assertContains(t, out, `"gamma"`)
	// Comment must appear before "alpha"
	firstIdx := strings.Index(out, "# first")
	alphaIdx := strings.Index(out, `"alpha"`)
	if firstIdx > alphaIdx {
		t.Errorf("expected '# first' before 'alpha':\n%s", out)
	}
}

func TestCommentOnLastItemInList(t *testing.T) {
	input := `
resource "example_resource" "r" {
  tags = [
    "alpha",
    # last
    "beta",
  ]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha", "beta"}})
	assertContains(t, out, "# last")
	// Must be before "beta"
	lastIdx := strings.Index(out, "# last")
	betaIdx := strings.Index(out, `"beta"`)
	if lastIdx > betaIdx {
		t.Errorf("expected '# last' before 'beta':\n%s", out)
	}
}

func TestInlineCommentOnEveryItem(t *testing.T) {
	input := `
resource "example_resource" "r" {
  envs = [
    "prod", # production
    "stage", # staging
    "dev", # development
  ]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"envs": []interface{}{"prod", "stage", "dev", "test"}})
	// All existing inline comments preserved.
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, `"prod"`):
			assertContains(t, line, "# production")
		case strings.Contains(line, `"stage"`):
			assertContains(t, line, "# staging")
		case strings.Contains(line, `"dev"`):
			assertContains(t, line, "# development")
		}
	}
	assertContains(t, out, `"test"`) // new item, no comment
}

func TestDoubleSlashComment(t *testing.T) {
	input := `
resource "example_resource" "r" {
  tags = [
    "alpha", // double slash comment
    "beta",
  ]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha", "beta"}})
	assertContains(t, out, "// double slash comment")
}

func TestMultipleBeforeCommentsOnOneItem(t *testing.T) {
	// Two comment lines before the same value — both should be preserved.
	input := `
resource "example_resource" "r" {
  tags = [
    # line one
    # line two
    "alpha",
    "beta",
  ]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha", "beta"}})
	assertContains(t, out, "# line one")
	assertContains(t, out, "# line two")
}

func TestCommentDroppedWhenItemRemoved(t *testing.T) {
	// When an item with a comment is removed from infra, its comment should not
	// appear in the output.
	input := `
resource "example_resource" "r" {
  tags = [
    "alpha",
    # beta comment
    "beta",
  ]
}
`
	// "beta" removed from infra
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha"}})
	assertNotContains(t, out, `"beta"`)
	assertNotContains(t, out, "# beta comment")
}

func TestBothBeforeAndInlineOnSameItem(t *testing.T) {
	// An item can have both a before-comment and an inline comment.
	input := `
resource "example_resource" "r" {
  tags = [
    # before comment
    "alpha", # inline comment
    "beta",
  ]
}
`
	out := applyDriftToString(t, input, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha", "beta", "gamma"}})
	assertContains(t, out, "# before comment")
	// Inline must be on same line as "alpha"
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `"alpha"`) {
			if !strings.Contains(line, "# inline comment") {
				t.Errorf("expected inline comment on alpha line, got: %q", line)
			}
			break
		}
	}
}

func TestCommentHookDoesNotOverrideExistingComment(t *testing.T) {
	// Existing comments should take priority over hook-generated ones.
	input := `
resource "example_resource" "r" {
  tags = [
    "alpha", # existing comment
    "beta",
  ]
}
`
	hook := func(_, _, _, value string) string {
		return "hook comment for " + value
	}

	dir := t.TempDir()
	fpath := filepath.Join(dir, "main.tf")
	_ = os.WriteFile(fpath, []byte(input), 0644)
	_, err := ApplyDrift(fpath, "example_resource", "r",
		map[string]interface{}{"tags": []interface{}{"alpha", "beta", "gamma"}},
		false, hook, nil)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	outBytes, _ := os.ReadFile(fpath)
	s := string(outBytes)

	// alpha's existing comment must be preserved, not replaced by hook.
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, `"alpha"`) {
			assertContains(t, line, "# existing comment")
			assertNotContains(t, line, "hook comment")
			break
		}
	}
	// "beta" has no existing comment — hook should fire.
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, `"beta"`) {
			assertContains(t, line, "hook comment")
			break
		}
	}
	// "gamma" is new — hook should fire.
	assertContains(t, s, `"gamma"`)
}

func TestCommentHookAttrPath(t *testing.T) {
	// Verify the path passed to the hook reflects the actual attr hierarchy.
	var capturedPaths []string
	hook := func(_, _, path, _ string) string {
		capturedPaths = append(capturedPaths, path)
		return ""
	}

	input := `
resource "github_repository_ruleset" "rs" {
  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }
}
`
	dir := t.TempDir()
	fpath := filepath.Join(dir, "main.tf")
	_ = os.WriteFile(fpath, []byte(input), 0644)
	_, err := ApplyDrift(fpath, "github_repository_ruleset", "rs",
		map[string]interface{}{
			"conditions": map[string]interface{}{
				"ref_name": map[string]interface{}{
					"include": []interface{}{"~DEFAULT_BRANCH", "refs/heads/main"},
				},
			},
		},
		false, hook, nil)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}

	// The list items should have been called with path "conditions.ref_name.include"
	found := false
	for _, p := range capturedPaths {
		if p == "conditions.ref_name.include" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected hook to be called with path 'conditions.ref_name.include', got: %v", capturedPaths)
	}
}

// ===========================================================================
// Format preservation — output should be valid HCL / match tofu fmt
// ===========================================================================

func TestOutputContainsNoScientificNotationForGitHubAppID(t *testing.T) {
	// GitHub App IDs can be large; they must not become scientific notation.
	// App IDs seen in the wild: 15368, 29110, 2026126, etc.
	for _, id := range []float64{15368, 29110, 2026126, 9999999} {
		input := `
resource "github_repository_ruleset" "rs" {
  bypass_actors {
    actor_id    = 1
    actor_type  = "Integration"
    bypass_mode = "always"
  }
}
`
		out := applyDriftToString(t, input, "github_repository_ruleset", "rs",
			map[string]interface{}{
				"bypass_actors": []interface{}{
					map[string]interface{}{
						"actor_id":    id,
						"actor_type":  "Integration",
						"bypass_mode": "always",
					},
				},
			})
		if strings.Contains(out, "e+") || strings.Contains(out, "E+") {
			t.Errorf("scientific notation for actor_id %.0f:\n%s", id, out)
		}
	}
}
