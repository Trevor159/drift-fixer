# drift-fixer

A CLI tool that automatically detects and corrects Terraform/OpenTofu configuration
drift. It runs a plan, reads the before/after diff from the plan JSON, edits the
relevant `.tf` files in-place using the `hclwrite` AST library, and validates the
result with a second plan.

Written in Go. No external CLI tools required — HCL is edited directly via the
[hashicorp/hcl](https://github.com/hashicorp/hcl) library.

---

## Table of Contents

- [Use as a GitHub Action](#use-as-a-github-action)
- [How It Works](#how-it-works)
- [Building](#building)
- [Usage](#usage)
- [Environment Variables](#environment-variables)
- [Comment Hook](#comment-hook)
- [Supported Features](#supported-features)
- [Known Limitations](#known-limitations)
- [Testing](#testing)
- [Project Structure](#project-structure)

---

## Use as a GitHub Action

This repository ships a composite action that builds the drift-fixer binary,
runs it against your Terraform/OpenTofu config, and either opens a pull
request, commits directly, or just reports drift.

### Quick start

```yaml
name: Fix Terraform Drift

on:
  schedule:
    - cron: "0 6 * * *"
  workflow_dispatch:

jobs:
  fix-drift:
    runs-on: ubuntu-latest
    permissions:
      contents: write       # push branches / commits
      pull-requests: write  # open PRs

    steps:
      - uses: actions/checkout@v4
      - uses: opentofu/setup-opentofu@v1
      - uses: mongodb-forks/drift-fixer@master
        with:
          path: ./infra
          mode: pr
```

Complete example workflows:
- [`examples/example-usage.yml`](examples/example-usage.yml) — `pr` mode with the default `GITHUB_TOKEN`.
- [`examples/example-usage-app-commit.yml`](examples/example-usage-app-commit.yml) — `commit` mode pushing as a GitHub App (for branch-ruleset bypass).

### Steps the action runs

1. Sets up Go and builds the drift-fixer binary from the action's source.
2. Runs `tofu init` (or `terraform init`) in `path`, retrying up to 3
   attempts with exponential backoff to absorb transient registry/CDN 502s.
3. Runs the binary, which produces drift fixes in `.tf` files.
4. Reverts any platform-specific h1 hashes `init` appended to
   `.terraform.lock.hcl` so they don't ride along in the PR.
5. Depending on `mode`, opens a PR, commits in place, or just reports.

The step fails the workflow on any non-zero exit from the binary — including
the case where drift remained after the fix attempt — so partial-state PRs
never ship.

### Inputs

| Name | Default | Description |
|---|---|---|
| `path` | `.` | Directory containing your `.tf` files. |
| `tf-bin` | `tofu` | Binary used for plan/init. Set to `terraform` for Terraform. |
| `mode` | `pr` | `pr` opens a pull request, `commit` pushes directly to the current branch, `dry-run` reports without writing. |
| `verbose` | `false` | Print every attribute and block change as it is applied. |
| `pr-base` | repository default branch | Base branch the PR targets. Required when the workflow runs on a detached HEAD (e.g. scheduled runs); the default is usually correct. |
| `pr-branch` | `drift-fixer/fix-<run_id>` | Branch name used in `mode: pr`. |
| `pr-title` | `fix: sync Terraform config with infrastructure drift` | Pull request title. |
| `pr-draft` | `true` | Open the PR as a draft. Set to `"false"` for ready-for-review. |
| `commit-message` | `fix: sync Terraform config with infrastructure drift` | Commit message used in both `pr` and `commit` modes. |
| `git-user-name` | `github-actions[bot]` | Commit author/committer name in `commit` mode. Override when pushing as a GitHub App. |
| `git-user-email` | `github-actions[bot]@users.noreply.github.com` | Commit author/committer email in `commit` mode. |
| `token` | `${{ github.token }}` | Token used for `gh` CLI calls (e.g. PR creation). For push auth, see "Pushing as a GitHub App" below. |

### Outputs

| Name | Description |
|---|---|
| `drift-detected` | `'true'` if drift was found (and fixed), `'false'` otherwise. |
| `pr-url` | URL of the opened PR when `mode: pr`. Empty otherwise. |

### Modes

- **`pr`** — fix drift on a new branch, open a (by default) draft PR. The PR
  is only opened when the post-fix validation plan reports no remaining drift.
- **`commit`** — fix drift and push directly to the current branch using the
  configured commit message. No PR is opened.
- **`dry-run`** — run plan and report what would change without writing
  anything. The action never opens a PR or commits in this mode.

### Pushing as a GitHub App

When you need the commit to come from a GitHub App — typically so the App can
be added to a branch ruleset bypass list — mint an installation token in the
workflow, hand it to `actions/checkout` (so `git push` is authenticated as the
App), and override the commit identity:

```yaml
- name: Mint App token
  id: app-token
  uses: actions/create-github-app-token@v2
  with:
    app-id: ${{ secrets.DRIFT_FIXER_APP_ID }}
    private-key: ${{ secrets.DRIFT_FIXER_APP_PRIVATE_KEY }}

- uses: actions/checkout@v4
  with:
    token: ${{ steps.app-token.outputs.token }}   # so git push is authenticated as the App

- uses: opentofu/setup-opentofu@v1

- uses: mongodb-forks/drift-fixer@master
  with:
    path: ./infra
    mode: commit
    token: ${{ steps.app-token.outputs.token }}
    git-user-name:  "${{ steps.app-token.outputs.app-slug }}[bot]"
    git-user-email: "${{ steps.app-token.outputs.installation-id }}+${{ steps.app-token.outputs.app-slug }}[bot]@users.noreply.github.com"
```

The push is authenticated by whatever credentials `actions/checkout`
persisted, so passing the App token there is what makes the bypass match
work. The `git-user-name` / `git-user-email` inputs control how the commit
is *displayed* — set them to the App's identity so commit listings show the
App as author.

---

## How It Works

1. **Plan** — runs `tofu plan -out <tmp>` then `tofu show -json <tmp>` in the
   target directory. Reads two arrays from the JSON output:
   - `resource_changes[*].change.{before,after}` — for each `update`/`replace`,
     diffs every attribute and collects keys where `before != after` into a
     `ResourceDrift`. The post-refresh `before` value (real infra) is what
     gets written back to config.
   - `resource_drift[*]` — surfaces resources that disappeared from real
     infra (e.g. deleted via a provider's web console). These appear in
     `resource_changes` as `create`, which the loop above skips, so the
     planner cross-references and emits a `Delete: true` drift only when
     `resource_changes` plans to recreate the resource (i.e. config still
     has the block).

2. **Find** — parses every `.tf` file in the project directory using the
   `hclsyntax` AST to locate which file contains each drifted resource block.

3. **Edit** — opens the file with `hclwrite`, walks the block tree, and applies
   each drifted attribute:
   - Scalar attributes are set with `SetAttributeValue` / `SetAttributeRaw`.
   - List attributes with more than one item are formatted one-item-per-line.
   - Block-typed values (nested blocks) are recursively synced.
   - Resources deleted from infra are removed from config entirely, along
     with any `import { to = <addr> }` block targeting them.
   - The output is run through `hclwrite.Format` (equivalent to `tofu fmt`).

4. **Validate** — runs a second plan. If no drift remains, exits 0. If drift
   persists, exits 1 with a summary.

---

## Building

**Requirements**

- Go 1.22+ (`go version`)
- OpenTofu or Terraform CLI on `$PATH` (default: `tofu`)
- `GOTOOLCHAIN=local` is required because `go.mod` pins 1.22 while some
  transitive dependencies were built with a newer toolchain declaration.

```bash
# Clone
git clone <repo-url>
cd drift-fixer

# Build binary to project root
cd go
GOTOOLCHAIN=local go build -o ../drift-fixer-go ./cmd/drift-fixer/

# Verify
cd ..
./drift-fixer-go -h
```

The resulting binary `drift-fixer-go` is self-contained with no runtime
dependencies.

---

## Usage

```
./drift-fixer-go [flags]

Flags:
  -path string
        Path to the Terraform/OpenTofu project directory (default ".")
  -tf-bin string
        Terraform/OpenTofu binary to use (default "tofu", or $DRIFT_FIXER_TF_BIN)
  -verbose
        Print every attribute and block change as it is applied
  -dry-run
        Detect and report drift without writing any files
```

### Examples

```bash
# Fix drift in the current directory using tofu
./drift-fixer-go

# Fix a specific project directory
./drift-fixer-go -path ./infra/github

# Use terraform instead of tofu
./drift-fixer-go -tf-bin terraform

# Preview what would change without editing files
./drift-fixer-go -dry-run -verbose

# Verbose mode shows every attribute as it is applied
./drift-fixer-go -verbose
```

### Typical output

```
Starting drift analysis in: /home/user/infra/github
Using CLI binary: tofu
🔍 Running plan to detect drift...
📊 Found 2 resource(s) with drift:
  - github_repository_ruleset.main
  - github_repository.app

📝 Applying fixes...

  Syncing github_repository_ruleset.main in rulesets.tf
  ✅ Fixed github_repository_ruleset.main in rulesets.tf

  Syncing github_repository.app in repos.tf
  ✅ Fixed github_repository.app in repos.tf

✅ Validating...
✅ Drift fixing completed successfully! No remaining drift.
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DRIFT_FIXER_TF_BIN` | `tofu` | Terraform/OpenTofu binary to invoke |
| `DRIFT_FIXER_COMMENT_SCRIPT` | *(unset)* | Path to an executable script that generates inline HCL comments (see [Comment Hook](#comment-hook)) |

---

## Comment Hook

When `DRIFT_FIXER_COMMENT_SCRIPT` is set, drift-fixer can annotate written
values with inline `# comments`. The script is invoked once per value that
needs a comment.

### How it works

drift-fixer uses a two-pass approach:

1. **Pass 1 — write the fix.** The resource is written to disk with all drift
   corrections applied and all existing comments preserved. No script is called
   yet.
2. **Pass 2 — annotate.** For each value in the drifted resource that has no
   existing comment, the script is called. Only the resources that had drift are
   processed — the rest of the file is untouched. Because the file has already
   been written, the script can read it to use surrounding context when deciding
   what comment to generate.

**Existing inline comments are always preserved** and take priority over
script-generated ones — the script is never called for a value that already
has a comment.

Built-in comment logic (e.g. annotating `repository_ids` entries with their
`data.github_repository.*` reference) runs in pass 1 and also suppresses the
script for those values.

### Variables passed to the script

| Variable | Example | Description |
|---|---|---|
| `DRIFT_RESOURCE_TYPE` | `github_repository_ruleset` | Terraform resource type |
| `DRIFT_RESOURCE_NAME` | `ruleset_main` | Terraform resource name |
| `DRIFT_ATTR_PATH` | `conditions.ref_name.include` | Dot-separated attribute path |
| `DRIFT_ATTR_VALUE` | `"~DEFAULT_BRANCH"` | Rendered value (strings are quoted) |
| `DRIFT_FILE_PATH` | `/path/to/main.tf` | Absolute path of the file already written to disk |

All environment variables from the parent process (including `GITHUB_TOKEN`,
`GITHUB_WORKSPACE`, etc.) are also inherited.

### Script contract

- Print a single line to **stdout** — the comment text, without `#`.
- Print **nothing** (or exit non-zero) to add no comment.
- Stderr is captured and shown on error (or in verbose mode).

### Example script

```bash
#!/usr/bin/env bash
# Annotate GitHub branch patterns with a human-readable description.
# $DRIFT_FILE_PATH is available if you need to read the full config for context.

case "$DRIFT_ATTR_VALUE" in
  '"~DEFAULT_BRANCH"') echo "the default branch" ;;
  '"~ALL"')            echo "all branches" ;;
  '"refs/heads/releases/**/*"') echo "release branches" ;;
esac
```

With this script active, a synced `include` list might look like:

```hcl
include = [
  "~DEFAULT_BRANCH", # the default branch
  "refs/heads/releases/**/*", # release branches
  "refs/heads/meep",
]
```

---

## Supported Features

### Attribute types

| Type | Supported | Notes |
|---|---|---|
| String | ✅ | Any content including slashes, regex, special chars |
| Boolean | ✅ | |
| Integer | ✅ | Large integers (e.g. GitHub App IDs) rendered as integers, never scientific notation |
| Float | ✅ | |
| String list | ✅ | Multi-line for >1 item, single-line for 1 item |
| Number list | ✅ | Same formatting rules |
| Nested block | ✅ | Recursively synced to arbitrary depth |
| Repeated blocks | ✅ | e.g. multiple `bypass_actors {}` |
| Null value | ✅ | Silently skipped |

### Block operations

Real infra is the source of truth: drift-fixer makes config match it
exactly, regardless of which side has more or fewer blocks.

| Operation | Behaviour |
|---|---|
| Add missing block | If a block type is entirely absent from config, all instances from infra are added |
| Sync existing block | Attributes inside existing blocks are updated in-place |
| Append missing entries | If config has *fewer* instances of a repeated block than infra, the missing entries are appended |
| Remove excess blocks | If config has *more* instances of a repeated block than infra, the extras are removed |
| Remove empty block type | If infra returns `[]` and config has blocks of that type, all are removed |
| Skip empty scalar list | If infra returns `[]` and no blocks of that type exist, it is ignored (not written as `= []`) |
| Delete resource | If a resource is deleted from infra (`actions: ["delete"]`), the entire `resource {}` block is removed from config |

### List formatting

```hcl
# Single item — stays on one line
topics = ["terraform"]

# Multiple items — one per line, `tofu fmt`-compliant indentation
topics = [
  "terraform",
  "go",
  "github",
]
```

### Comment preservation

Comments in list attributes survive drift syncs:

- **Before-line comments** (`# comment` on its own line before a value)
- **Inline comments** (`# comment` at the end of the same line as a value)
- **`//` style** comments
- Comments are keyed by **rendered value content**, not position — they follow
  their value even if the list is reordered or items are inserted before them.
- Comments for **removed items** are dropped.

```hcl
include = [
  # default branch — always included
  "~DEFAULT_BRANCH", # added automatically by provider
  "refs/heads/releases/**/*", # release train
]
```

### Resources tested against (GitHub provider)

| Resource | Notes |
|---|---|
| `github_repository` | `description`, `visibility`, `topics`, all boolean flags, `security_and_analysis` nested blocks, `squash_merge_commit_title/message`, `merge_commit_title/message` |
| `github_repository_ruleset` | `enforcement`, `target`, `conditions.ref_name.{include,exclude}`, `bypass_actors`, all `rules.*` sub-blocks listed below |
| `rules.pull_request` | `allowed_merge_methods`, `required_approving_review_count`, all boolean flags |
| `rules.required_status_checks` | `required_check` repeated blocks (add, remove, sync) |
| `rules.required_deployments` | `required_deployment_environments` list |
| `rules.branch_name_pattern` | `operator`, `pattern`, `name`, `negate` |
| `rules.tag_name_pattern` | same schema as `branch_name_pattern` |
| `rules.commit_message_pattern` | same schema |
| `rules.commit_author_email_pattern` | same schema |
| `rules.committer_email_pattern` | same schema |
| `rules.required_code_scanning` | `required_code_scanning_tool` repeated blocks |
| `rules.file_path_restriction` | `restricted_file_paths` list |
| `rules.file_extension_restriction` | `restricted_file_extensions` list |
| `rules.max_file_size` | `max_file_size` integer |
| `rules.max_file_path_length` | `max_file_path_length` integer |
| `rules.merge_queue` | all integer and string fields |

The tool is provider-agnostic — it works on any Terraform/OpenTofu resource. The
GitHub provider has been the primary test target.

---

## Known Limitations

- **Sensitive attributes** — attributes marked sensitive in the plan JSON are
  skipped (the provider redacts their values, so drift-fixer cannot know the
  correct value).
- **`after_unknown` attributes** — attributes whose post-change value is not
  known at plan time are skipped.
- **Multiple resources with the same type+name** — only the first match in a
  file is edited. This is a theoretical edge case since Terraform requires unique
  addresses.
- **`for_each` / `count` meta-arguments** — resources managed with `count` or
  `for_each` are located by address label. The tool edits the block body directly;
  it does not currently modify the `count` or `for_each` expression itself.
- **Comment hook is synchronous** — the script is exec'd once per value that
  needs a comment (pass 2 only). Values with existing comments are skipped.
  For large syncs with many uncommented list items, a slow script will slow the overall run.

---

## Testing

All unit tests live under `go/internal/` and use only the standard library
(`testing` package). No real cloud credentials or live API calls are made.

```bash
# Run everything (75 tests across 3 files)
cd go
GOTOOLCHAIN=local go test ./...

# Just the editor or planner suites
GOTOOLCHAIN=local go test ./internal/editor/ -v
GOTOOLCHAIN=local go test ./internal/planner/ -v

# Run a specific test by name
GOTOOLCHAIN=local go test ./internal/editor/ -v -run TestBypassActorModeChange
```

### Test files

| File | Count | Coverage |
|---|---|---|
| `internal/editor/editor_test.go` | 18 | Core edit operations, multi-line lists, comment preservation, hook |
| `internal/editor/editor_extended_test.go` | 48 | GitHub provider shapes, list mutations, deep nesting, comment edge cases, hook path verification |
| `internal/planner/planner_test.go` | 9 | Plan-JSON parsing: attribute drift, polymorphic `before_sensitive`/`after_unknown`, sensitive-attr filtering, resource_drift delete handling, post-fix validation idempotency |

### Integration test (`test.sh`)

Requires a real GitHub token and the `examples/` directory pointing at a live
repository:

```bash
# Set up credentials
echo 'export GITHUB_TOKEN=ghp_...' > .env

# Run full end-to-end: build → checkout clean main.tf → plan → fix → validate
./test.sh

# Dry run
./test.sh --dry-run
```

The script:
1. Builds the binary from source.
2. Resets `examples/main.tf` to HEAD with `git checkout`.
3. Runs `drift-fixer-go -path examples/ -verbose`.
4. Exits 0 only if the second plan confirms no remaining drift.

---

## Project Structure

```
drift-fixer/
├── action.yml              # composite GitHub Action definition
├── drift-fixer-go          # compiled binary (gitignored)
├── test.sh                 # integration test script
├── examples/
│   ├── main.tf             # test fixture (github provider resources)
│   └── example-usage.yml   # workflow consumers can copy into their repo
└── go/
    ├── go.mod
    ├── go.sum
    ├── cmd/
    │   └── drift-fixer/
    │       └── main.go     # CLI entry point: flags, orchestration, validate
    └── internal/
        ├── planner/
        │   ├── planner.go      # runs plan+show-json, parses plan JSON, returns ResourceDrift list
        │   └── planner_test.go # unit tests for parsePlanJSON
        ├── finder/
        │   └── finder.go       # parses .tf AST to locate which file contains each resource
        └── editor/
            ├── editor.go               # hclwrite-based editor: ApplyDrift, RemoveResource, syncBody
            ├── hook.go                 # CommentHook type + built-in comment generators
            ├── editor_test.go          # core unit tests (18)
            └── editor_extended_test.go # extended GitHub provider tests (48)
```

### Package responsibilities

**`planner`**
- Runs `<tf-bin> plan -out <tmp>` followed by `<tf-bin> show -json <tmp>`.
- `parsePlanJSON` (unit-tested independently of tofu) does the analysis:
  - `resource_changes` `update`/`replace` → walks `change.before` vs
    `change.after`, collects every key where the values differ; skips
    sensitive keys and `after_unknown` markers.
  - `resource_changes` `delete` → emits `ResourceDrift{Delete: true}`.
  - `resource_drift` `delete` → emits `ResourceDrift{Delete: true}` *only*
    when `resource_changes` for the same address plans `create` (i.e. config
    still has the block). Without this guard, the post-fix validation plan
    would re-flag the same delete every time refresh runs.

**`finder`**
- Walks the project directory for `*.tf` files.
- Parses each file with `hclsyntax.ParseConfig` (full AST, no regex).
- Returns a map of `resourceAddress → filePath`.

**`editor`**
- `ApplyDrift` — main entry point; opens file, finds resource block, calls
  `syncBody`, writes formatted output.
- `RemoveResource` — removes the entire `resource {}` block, plus any
  `import { to = <addr> }` block in the same file targeting it (otherwise
  tofu plan errors with "Configuration for import target does not exist").
- `syncBody` — recursive; handles empty lists, block vs scalar distinction,
  block count policy, path accumulation for hooks.
- `setAttributeVal` — scalar/list writer; extracts and re-injects comments;
  calls hook for values with no existing comment.
- `extractItemComments` — token-stream parser that maps rendered value string →
  `{before, inline}` comment tokens. Keyed by value content so comments survive
  list reordering.
- `multilineListTokens` — builds `hclwrite.Tokens` for a multi-line list,
  re-inserting preserved comments and hook-generated comments in the right spots.

**`hook`**
- `CommentHook` — function type `(rType, rName, path, value, filePath) → comment`.
- `LoadCommentHook` — reads `DRIFT_FIXER_COMMENT_SCRIPT`; returns a hook that exec's the script
  with context via env vars (including `DRIFT_FILE_PATH`), or `nil` if unset.
- `ComposeHooks` — chains two hooks; tries primary first, falls back to secondary.
