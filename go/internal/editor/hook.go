package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CommentHook is a function called whenever drift-fixer writes a value (scalar
// attribute or individual list item). It receives the resource context, the
// rendered value string, and the path of the .tf file being edited (already
// written to disk at call time so the script can read it for context).
// It should return a comment body (without the leading "# "), or an empty
// string if no comment is desired.
//
// A nil CommentHook means no hook is active.
type CommentHook func(resourceType, resourceName, attrPath, value, filePath string) string

// ComposeHooks returns a CommentHook that tries primary first and falls back
// to secondary. Either argument may be nil.
func ComposeHooks(primary, secondary CommentHook) CommentHook {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return func(resourceType, resourceName, attrPath, value, filePath string) string {
		if c := primary(resourceType, resourceName, attrPath, value, filePath); c != "" {
			return c
		}
		return secondary(resourceType, resourceName, attrPath, value, filePath)
	}
}

// LoadCommentHook reads the DRIFT_FIXER_COMMENT_SCRIPT environment variable.
// If set, it returns a CommentHook that invokes that script for every written
// value. The script receives context via environment variables:
//
//	DRIFT_RESOURCE_TYPE  – e.g. "github_repository_ruleset"
//	DRIFT_RESOURCE_NAME  – e.g. "ruleset_15577636"
//	DRIFT_ATTR_PATH      – e.g. "conditions.ref_name.include"
//	DRIFT_ATTR_VALUE     – e.g. "~DEFAULT_BRANCH"
//	DRIFT_FILE_PATH      – absolute path to the .tf file (already written to disk)
//
// Whatever the script prints to stdout (trimmed) is used as the comment text.
// An empty output means no comment is added.
// If DRIFT_FIXER_COMMENT_SCRIPT is unset, LoadCommentHook returns nil.
func LoadCommentHook(verbose bool) CommentHook {
	script := os.Getenv("DRIFT_FIXER_COMMENT_SCRIPT")
	if script == "" {
		if verbose {
			fmt.Println("[hook] DRIFT_FIXER_COMMENT_SCRIPT is unset; comment hook disabled")
		}
		return nil
	}
	if verbose {
		fmt.Printf("[hook] DRIFT_FIXER_COMMENT_SCRIPT=%s — comment hook enabled\n", script)
		if info, err := os.Stat(script); err != nil {
			fmt.Printf("[hook] WARNING: stat %s failed: %v (the hook will fire but every call will fail)\n", script, err)
		} else if info.Mode()&0o111 == 0 {
			fmt.Printf("[hook] WARNING: %s is not executable (mode %v) — exec will fail\n", script, info.Mode())
		}
	}
	return func(resourceType, resourceName, attrPath, value, filePath string) string {
		cmd := exec.Command(script)
		cmd.Env = append(os.Environ(),
			"DRIFT_RESOURCE_TYPE="+resourceType,
			"DRIFT_RESOURCE_NAME="+resourceName,
			"DRIFT_ATTR_PATH="+attrPath,
			"DRIFT_ATTR_VALUE="+value,
			"DRIFT_FILE_PATH="+filePath,
		)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		comment := strings.TrimSpace(string(out))
		if verbose {
			fmt.Printf("[hook] call %s.%s path=%s value=%s -> comment=%q\n",
				resourceType, resourceName, attrPath, value, comment)
			if s := strings.TrimSpace(stderr.String()); s != "" {
				fmt.Printf("[hook]   script stderr: %s\n", s)
			}
		}
		if err != nil {
			msg := fmt.Sprintf("[hook] script failed for %s.%s path=%s value=%s: %v",
				resourceType, resourceName, attrPath, value, err)
			if s := strings.TrimSpace(stderr.String()); s != "" {
				msg += "\n[hook]   script stderr: " + s
			}
			fmt.Fprintln(os.Stderr, msg)
			return ""
		}
		return comment
	}
}
