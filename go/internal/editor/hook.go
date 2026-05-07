package editor

// CommentHook is a function called whenever drift-fixer writes a value (scalar
// attribute or individual list item). It receives the resource context and the
// rendered value string. It should return a comment body (without the leading
// "# "), or an empty string if no comment is desired.
//
// A nil CommentHook means no hook is active.
type CommentHook func(resourceType, resourceName, attrPath, value string) string

// BuildRepoIDHook returns a CommentHook that annotates integer values in
// github_actions_organization_permissions enabled_repositories_config.repository_ids
// with a comment showing the corresponding data source reference, e.g.:
//
//	204896, # data.github_repository.mms.repo_id
//
// repoIDs maps the decimal-string repository ID to the data source name
// (e.g. "204896" → "mms"), as returned by planner.Run.
// Returns nil when repoIDs is empty.
func BuildRepoIDHook(repoIDs map[string]string) CommentHook {
	if len(repoIDs) == 0 {
		return nil
	}
	return func(resourceType, _, attrPath, value string) string {
		if resourceType != "github_actions_organization_permissions" {
			return ""
		}
		if attrPath != "enabled_repositories_config.repository_ids" {
			return ""
		}
		if name, ok := repoIDs[value]; ok {
			return "data.github_repository." + name + ".repo_id"
		}
		return ""
	}
}
