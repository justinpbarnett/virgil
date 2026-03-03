package fs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// file.txt
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)

	// sub/code.go
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "code.go"), []byte("package sub"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "pipe.yaml"), []byte("name: test"), 0o644)

	// empty/
	os.MkdirAll(filepath.Join(dir, "empty"), 0o755)

	return dir
}

// --- list action ---

func TestListRootDirectory(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "."})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	testutil.AssertEnvelope(t, result, "fs", "list")
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
	entries, ok := result.Content.([]FileInfo)
	if !ok {
		t.Fatalf("expected []FileInfo, got %T", result.Content)
	}
	if len(entries) != 3 { // file.txt, sub, empty
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestListWithGlobPattern(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "sub", "pattern": "*.go"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	entries := result.Content.([]FileInfo)
	if len(entries) != 1 {
		t.Fatalf("expected 1 match, got %d", len(entries))
	}
	if entries[0].Name != "code.go" {
		t.Errorf("expected code.go, got %s", entries[0].Name)
	}
}

func TestListWithRecursiveGlob(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": ".", "pattern": "**/*.yaml"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	entries := result.Content.([]FileInfo)
	if len(entries) != 1 {
		t.Fatalf("expected 1 match, got %d", len(entries))
	}
	if entries[0].Name != "pipe.yaml" {
		t.Errorf("expected pipe.yaml, got %s", entries[0].Name)
	}
}

func TestListEmptyDirectory(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "empty"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	entries := result.Content.([]FileInfo)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestListNonexistentDirectory(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "nope"})

	testutil.AssertFatalError(t, result)
}

func TestListOutsideAllowedRoots(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "/etc"})

	testutil.AssertFatalError(t, result)
}

func TestListFilePathErrors(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "file.txt"})

	testutil.AssertFatalError(t, result)
}

// --- read action ---

func TestReadExistingFile(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "file.txt"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	testutil.AssertEnvelope(t, result, "fs", "read")
	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	content, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if content != "hello" {
		t.Errorf("expected 'hello', got %q", content)
	}
}

func TestReadNonexistentFile(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "nope.txt"})

	testutil.AssertFatalError(t, result)
}

func TestReadDirectoryPath(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "sub"})

	testutil.AssertFatalError(t, result)
}

func TestReadOutsideAllowedRoots(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "/etc/passwd"})

	testutil.AssertFatalError(t, result)
}

func TestReadEmptyFile(t *testing.T) {
	dir := testDir(t)
	os.WriteFile(filepath.Join(dir, "empty.txt"), []byte{}, 0o644)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "empty.txt"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	content := result.Content.(string)
	if content != "" {
		t.Errorf("expected empty string, got %q", content)
	}
}

func TestReadFileTooLarge(t *testing.T) {
	dir := testDir(t)
	bigData := make([]byte, maxFileSize+1)
	for i := range bigData {
		bigData[i] = 'x'
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), bigData, 0o644)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "big.txt"})

	testutil.AssertFatalError(t, result)
}

func TestReadMissingPathFlag(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read"})

	testutil.AssertFatalError(t, result)
}

// --- write action ---

func TestWriteNewFile(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "new content"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "output.txt"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	testutil.AssertEnvelope(t, result, "fs", "write")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	// Verify file was written
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("expected 'new content', got %q", string(data))
	}

	// Verify WriteResult
	m, ok := result.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result.Content)
	}
	if bw, ok := m["bytes_written"].(int); !ok || bw != 11 {
		t.Errorf("expected bytes_written=11, got %v", m["bytes_written"])
	}
}

func TestWriteOverwritesExistingFile(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "overwritten"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "file.txt"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "file.txt"))
	if string(data) != "overwritten" {
		t.Errorf("expected 'overwritten', got %q", string(data))
	}
}

func TestWriteCreatesParentDirectories(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "nested"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "a/b/c/file.txt"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "file.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestWriteEmptyContent(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "marker.txt"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "marker.txt"))
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}

func TestWriteOutsideAllowedRoots(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "evil"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "/tmp/evil.txt"})

	testutil.AssertFatalError(t, result)
}

func TestWriteToSymlink(t *testing.T) {
	dir := testDir(t)
	target := filepath.Join(dir, "file.txt")
	link := filepath.Join(dir, "link.txt")
	os.Symlink(target, link)

	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "via link"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "link.txt"})

	testutil.AssertFatalError(t, result)
}

func TestWriteToExistingDirectory(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "stuff"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "sub"})

	testutil.AssertFatalError(t, result)
}

func TestWriteMissingPathFlag(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "stuff"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write"})

	testutil.AssertFatalError(t, result)
}

// --- security ---

func TestPathTraversalBeyondRoot(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "../../etc/passwd"})

	testutil.AssertFatalError(t, result)
}

func TestAbsolutePathOutsideRoots(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list", "path": "/usr/bin"})

	testutil.AssertFatalError(t, result)
}

func TestAbsolutePathInsideRoots(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": filepath.Join(dir, "file.txt")})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", result.Content)
	}
}

func TestAllowedExtraPaths(t *testing.T) {
	dir := testDir(t)
	extraDir := t.TempDir()
	os.WriteFile(filepath.Join(extraDir, "notes.txt"), []byte("my notes"), 0o644)

	handler := NewHandler(dir, []string{extraDir}, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": filepath.Join(extraDir, "notes.txt")})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content.(string) != "my notes" {
		t.Errorf("expected 'my notes', got %q", result.Content)
	}
}

// --- envelope compliance ---

func TestEnvelopeComplianceList(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "list"})

	testutil.AssertEnvelope(t, result, "fs", "list")
	if result.Args["action"] != "list" {
		t.Errorf("expected args[action]=list, got %s", result.Args["action"])
	}
}

func TestEnvelopeComplianceRead(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "read", "path": "file.txt"})

	testutil.AssertEnvelope(t, result, "fs", "read")
}

func TestEnvelopeComplianceWrite(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")
	input.Content = "test"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"action": "write", "path": "out.txt"})

	testutil.AssertEnvelope(t, result, "fs", "write")
}

// --- default action ---

func TestDefaultActionIsList(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	if result.Action != "list" {
		t.Errorf("expected action=list, got %s", result.Action)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}

func TestUnknownAction(t *testing.T) {
	dir := testDir(t)
	handler := NewHandler(dir, nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "delete"})

	testutil.AssertFatalError(t, result)
}
