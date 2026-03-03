package fs

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

const maxFileSize = 1 << 20 // 1MB

type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

func NewHandler(projectRoot string, allowedPaths []string, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	roots := make([]string, 0, 1+len(allowedPaths))
	if resolved, err := filepath.EvalSymlinks(projectRoot); err == nil {
		roots = append(roots, resolved)
	} else {
		roots = append(roots, filepath.Clean(projectRoot))
	}
	for _, p := range allowedPaths {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			roots = append(roots, resolved)
		} else {
			roots = append(roots, filepath.Clean(p))
		}
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		if action == "" {
			action = "list"
		}
		switch action {
		case "list":
			return handleList(projectRoot, roots, input, flags, logger)
		case "read":
			return handleRead(projectRoot, roots, flags, logger)
		case "write":
			return handleWrite(projectRoot, roots, input, flags, logger)
		default:
			out := envelope.New("fs", action)
			out.Args = flags
			out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s", action))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
	}
}

func resolvePath(projectRoot string, roots []string, rawPath string) (string, error) {
	var resolved string
	if filepath.IsAbs(rawPath) {
		resolved = filepath.Clean(rawPath)
	} else {
		resolved = filepath.Clean(filepath.Join(projectRoot, rawPath))
	}

	evaled, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		// If the path doesn't exist yet (for write), eval parent
		parent := filepath.Dir(resolved)
		if evaledParent, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
			evaled = filepath.Join(evaledParent, filepath.Base(resolved))
		} else {
			evaled = resolved
		}
	}

	for _, root := range roots {
		if evaled == root || strings.HasPrefix(evaled, root+string(filepath.Separator)) {
			return evaled, nil
		}
	}
	return "", fmt.Errorf("path %s is outside allowed roots: %v", evaled, roots)
}

func relPath(projectRoot, absPath string) string {
	rel, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

func handleList(projectRoot string, roots []string, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("fs", "list")
	out.Args = flags

	rawPath := flags["path"]
	if rawPath == "" {
		rawPath = "."
	}

	resolved, err := resolvePath(projectRoot, roots, rawPath)
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			out.Error = envelope.FatalError(fmt.Sprintf("directory not found: %s", rawPath))
		} else {
			out.Error = envelope.FatalError(fmt.Sprintf("stat failed: %v", err))
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}
	if !info.IsDir() {
		out.Error = envelope.FatalError(fmt.Sprintf("path is a file, use action=read: %s", rawPath))
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	pattern := flags["pattern"]
	var entries []FileInfo

	if pattern != "" {
		if strings.Contains(pattern, "**") {
			entries, err = globRecursive(resolved, projectRoot, pattern)
		} else {
			entries, err = globSimple(resolved, projectRoot, pattern)
		}
		if err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("invalid glob pattern: %s: %v", pattern, err))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
	} else {
		dirEntries, readErr := os.ReadDir(resolved)
		if readErr != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("reading directory: %v", readErr))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
		for _, de := range dirEntries {
			fi, fiErr := de.Info()
			if fiErr != nil {
				continue
			}
			absPath := filepath.Join(resolved, de.Name())
			entries = append(entries, FileInfo{
				Name:    de.Name(),
				Path:    relPath(projectRoot, absPath),
				Size:    fi.Size(),
				ModTime: fi.ModTime(),
				IsDir:   de.IsDir(),
			})
		}
	}

	if entries == nil {
		entries = []FileInfo{}
	}

	logger.Info("listed", "path", rawPath, "count", len(entries))
	out.Content = entries
	out.ContentType = envelope.ContentList
	out.Duration = time.Since(out.Timestamp)
	return out
}

func globSimple(dir, projectRoot, pattern string) ([]FileInfo, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil, err
	}
	var result []FileInfo
	for _, m := range matches {
		fi, statErr := os.Stat(m)
		if statErr != nil {
			continue
		}
		result = append(result, FileInfo{
			Name:    filepath.Base(m),
			Path:    relPath(projectRoot, m),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
			IsDir:   fi.IsDir(),
		})
	}
	return result, nil
}

func globRecursive(dir, projectRoot, pattern string) ([]FileInfo, error) {
	// For ** patterns, walk the tree and match each relative path
	var result []FileInfo
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip errors
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if matchDoublestar(pattern, rel) {
			fi, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			result = append(result, FileInfo{
				Name:    d.Name(),
				Path:    relPath(projectRoot, path),
				Size:    fi.Size(),
				ModTime: fi.ModTime(),
				IsDir:   d.IsDir(),
			})
		}
		return nil
	})
	return result, err
}

// matchDoublestar implements simple ** glob matching.
// It splits the pattern on "**/" or "/**" segments and checks that each
// non-** segment matches via filepath.Match at some position in the path.
func matchDoublestar(pattern, path string) bool {
	// Split pattern into segments
	patParts := strings.Split(pattern, string(filepath.Separator))
	pathParts := strings.Split(path, string(filepath.Separator))
	return matchParts(patParts, pathParts)
}

func matchParts(patParts, pathParts []string) bool {
	if len(patParts) == 0 {
		return len(pathParts) == 0
	}

	if patParts[0] == "**" {
		rest := patParts[1:]
		// ** can match zero or more path segments
		for i := 0; i <= len(pathParts); i++ {
			if matchParts(rest, pathParts[i:]) {
				return true
			}
		}
		return false
	}

	if len(pathParts) == 0 {
		return false
	}

	matched, err := filepath.Match(patParts[0], pathParts[0])
	if err != nil || !matched {
		return false
	}
	return matchParts(patParts[1:], pathParts[1:])
}

func handleRead(projectRoot string, roots []string, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("fs", "read")
	out.Args = flags

	rawPath := flags["path"]
	if rawPath == "" || rawPath == "." {
		out.Error = envelope.FatalError("path is required for read action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	resolved, err := resolvePath(projectRoot, roots, rawPath)
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			out.Error = envelope.FatalError(fmt.Sprintf("file not found: %s", rawPath))
		} else {
			out.Error = envelope.FatalError(fmt.Sprintf("stat failed: %v", err))
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}
	if info.IsDir() {
		out.Error = envelope.FatalError(fmt.Sprintf("path is a directory, use action=list: %s", rawPath))
		out.Duration = time.Since(out.Timestamp)
		return out
	}
	if info.Size() > maxFileSize {
		out.Error = envelope.FatalError(fmt.Sprintf("file too large (%d bytes), max %d: %s", info.Size(), maxFileSize, rawPath))
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsPermission(err) {
			out.Error = envelope.FatalError(fmt.Sprintf("permission denied: %s", rawPath))
		} else {
			out.Error = envelope.FatalError(fmt.Sprintf("reading file: %v", err))
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	// Check if text: valid UTF-8 and no null bytes in first 8KB
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	if !utf8.Valid(data[:checkLen]) || bytes.IndexByte(data[:checkLen], 0) >= 0 {
		out.Error = envelope.FatalError(fmt.Sprintf("binary file not supported: %s", rawPath))
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("read", "path", rawPath, "size", len(data))
	out.Content = string(data)
	out.ContentType = envelope.ContentText
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleWrite(projectRoot string, roots []string, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("fs", "write")
	out.Args = flags

	rawPath := flags["path"]
	if rawPath == "" || rawPath == "." {
		out.Error = envelope.FatalError("path is required for write action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	// Compute the clean path before symlink resolution so we can check for symlinks
	var cleanPath string
	if filepath.IsAbs(rawPath) {
		cleanPath = filepath.Clean(rawPath)
	} else {
		cleanPath = filepath.Clean(filepath.Join(projectRoot, rawPath))
	}

	// Check if target is a symlink before resolving (Lstat does not follow symlinks)
	if linfo, lErr := os.Lstat(cleanPath); lErr == nil {
		if linfo.Mode()&os.ModeSymlink != 0 {
			out.Error = envelope.FatalError(fmt.Sprintf("write target is a symlink: %s", rawPath))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
		if linfo.IsDir() {
			out.Error = envelope.FatalError(fmt.Sprintf("path is an existing directory: %s", rawPath))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
	}

	resolved, err := resolvePath(projectRoot, roots, rawPath)
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	// Create parent directories
	parentDir := filepath.Dir(resolved)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("creating directories: %v", err))
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	content := envelope.ContentToText(input.Content, input.ContentType)

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		if os.IsPermission(err) {
			out.Error = envelope.FatalError(fmt.Sprintf("permission denied: %s", rawPath))
		} else {
			out.Error = envelope.FatalError(fmt.Sprintf("writing file: %v", err))
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("wrote", "path", rawPath, "bytes", len(content))
	out.Content = map[string]any{
		"path":          relPath(projectRoot, resolved),
		"bytes_written": len(content),
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}
