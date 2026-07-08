// Package main: security validation for input sanitization.
// Port of security.py — path traversal prevention, git URL validation,
// command injection blocking.
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var safeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

var allowedGitProtocols = []string{"https://", "http://", "git://"}

var shellBlacklist = []string{
	"rm -rf /",
	"mkfs",
	"dd if=",
	":(){ :|:& };:",
	"chmod 777 /",
	"curl.*|.*sh",
	"wget.*|.*sh",
}

// validateSafeName validates that a name is safe to use as directory/volume name.
// Rejects path traversal, path separators, null bytes, names starting with dot.
func validateSafeName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name cannot be empty")
	}
	if !safeNameRe.MatchString(name) {
		return "", fmt.Errorf("invalid name '%s': must be alphanumeric with dots, dashes, or underscores, 1-64 chars, cannot start with dot", name)
	}
	return name, nil
}

// validatePathSafe ensures a resolved path is contained within root.
func validatePathSafe(path, root string) (string, error) {
	resolved := resolvePath(path)
	rootResolved := resolvePath(root)
	if !strings.HasPrefix(resolved+string(filepath.Separator), rootResolved+string(filepath.Separator)) && resolved != rootResolved {
		return "", fmt.Errorf("path '%s' escapes allowed root '%s'", path, root)
	}
	return resolved, nil
}

// validateGitURL validates that a git URL uses an allowed protocol.
// Only https://, http://, and git:// are allowed.
func validateGitURL(url string) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("git URL cannot be empty")
	}
	allowed := false
	for _, proto := range allowedGitProtocols {
		if strings.HasPrefix(url, proto) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("invalid git URL '%s': only %s are allowed", url, strings.Join(allowedGitProtocols, ", "))
	}
	// Reject URLs with embedded credentials (user:pass@host)
	parts := strings.SplitN(url, "://", 2)
	if len(parts) == 2 {
		hostPart := strings.SplitN(parts[1], "/", 2)[0]
		if strings.Contains(hostPart, "@") {
			return "", fmt.Errorf("git URL with embedded credentials is not allowed")
		}
	}
	return url, nil
}

// validateCommand does basic command validation for exec_in_container.
func validateCommand(cmd string) (string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", fmt.Errorf("command cannot be empty")
	}
	for _, pattern := range shellBlacklist {
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			continue
		}
		if re.MatchString(cmd) {
			return "", fmt.Errorf("command contains blacklisted pattern")
		}
	}
	return cmd, nil
}

// sanitizeGitURLForName extracts a safe directory name from a git URL.
func sanitizeGitURLForName(url string) string {
	url = strings.TrimRight(url, "/")
	parts := strings.Split(url, "/")
	name := strings.TrimSuffix(parts[len(parts)-1], ".git")
	// Replace any non-safe char with dash
	reg := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	name = reg.ReplaceAllString(name, "-")
	// Ensure it doesn't start with dot or dash
	if len(name) > 0 && (name[0] == '.' || name[0] == '-') {
		name = "app-" + name
	}
	if name == "" {
		name = "unnamed-app"
	}
	return name
}

// resolvePath resolves symlinks and returns absolute path.
func resolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Path doesn't exist yet — use the abs path directly
		return abs
	}
	return resolved
}

// isGitInstalled checks if git is available on the system.
func isGitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}
