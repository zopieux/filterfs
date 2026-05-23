package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	ignore "github.com/sabhiram/go-gitignore"
)

func checkFUSE(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("Skipping test: /dev/fuse is not available (likely running in a sandboxed Nix build environment)")
	}
}

func TestFilterFSBasic(t *testing.T) {
	checkFUSE(t)

	sourceDir := t.TempDir()
	mountPoint := t.TempDir()

	// Populated hierarchy in source.
	files := map[string]string{
		"a":     "content a",
		"b":     "content b",
		"dir/a": "content dir/a",
		"dir/c": "content dir/c",
	}
	for relPath, content := range files {
		absPath := filepath.Join(sourceDir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ignoreContent := "/a\ndir/c\n"
	ignoreFile := filepath.Join(sourceDir, ".gitignore")
	if err := os.WriteFile(ignoreFile, []byte(ignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	gitign, err := ignore.CompileIgnoreFile(ignoreFile)
	if err != nil {
		t.Fatal(err)
	}

	var gitignPtr atomic.Pointer[ignore.GitIgnore]
	gitignPtr.Store(gitign)

	var st syscall.Stat_t
	if err := syscall.Stat(sourceDir, &st); err != nil {
		t.Fatal(err)
	}

	root := &filterNode{
		gitign: &gitignPtr,
	}
	root.LoopbackNode.RootData = &fs.LoopbackRoot{
		NewNode: newFilterNode,
		Path:    sourceDir,
		Dev:     uint64(st.Dev),
	}
	root.LoopbackNode.RootData.RootNode = root

	opts := &fs.Options{}
	opts.Name = "filterfs"
	opts.FsName = "filterfs"
	opts.DirectMount = true
	opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	if err := server.WaitMount(); err != nil {
		t.Fatal(err)
	}

	// "b" is visible and readable.
	bPath := filepath.Join(mountPoint, "b")
	if _, err := os.Stat(bPath); err != nil {
		t.Errorf("expected b to be visible: %v", err)
	} else {
		data, err := os.ReadFile(bPath)
		if err != nil {
			t.Errorf("expected to read b: %v", err)
		} else if string(data) != "content b" {
			t.Errorf("unexpected content of b: %s", string(data))
		}
	}

	// "a" is NOT visible (Lookup returns ENOENT).
	aPath := filepath.Join(mountPoint, "a")
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Errorf("expected a to be ignored (not found), got: %v", err)
	}

	// "dir/a" is visible and readable.
	diraPath := filepath.Join(mountPoint, "dir/a")
	if _, err := os.Stat(diraPath); err != nil {
		t.Errorf("expected dir/a to be visible: %v", err)
	} else {
		data, err := os.ReadFile(diraPath)
		if err != nil {
			t.Errorf("expected to read dir/a: %v", err)
		} else if string(data) != "content dir/a" {
			t.Errorf("unexpected content of dir/a: %s", string(data))
		}
	}

	// "dir/c" is NOT visible.
	dircPath := filepath.Join(mountPoint, "dir/c")
	if _, err := os.Stat(dircPath); !os.IsNotExist(err) {
		t.Errorf("expected dir/c to be ignored (not found), got: %v", err)
	}

	// Listing the root directory only lists "b" and "dir", and .gitignore if not ignored.
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		t.Fatal(err)
	}
	visible := make(map[string]bool)
	for _, entry := range entries {
		visible[entry.Name()] = true
	}
	if visible["a"] {
		t.Errorf("expected 'a' not to be listed in root directory")
	}
	if !visible["b"] {
		t.Errorf("expected 'b' to be listed in root directory")
	}
	if !visible["dir"] {
		t.Errorf("expected 'dir' to be listed in root directory")
	}

	// Listing "dir" directory only lists "a".
	dirEntries, err := os.ReadDir(filepath.Join(mountPoint, "dir"))
	if err != nil {
		t.Fatal(err)
	}
	dirVisible := make(map[string]bool)
	for _, entry := range dirEntries {
		dirVisible[entry.Name()] = true
	}
	if !dirVisible["a"] {
		t.Errorf("expected 'dir/a' to be listed in dir directory")
	}
	if dirVisible["c"] {
		t.Errorf("expected 'dir/c' not to be listed in dir directory")
	}

	// Verify that writing is indeed BLOCKED (read-only mount).
	err = os.WriteFile(bPath, []byte("read only try"), 0644)
	if err == nil {
		t.Errorf("expected write to fail on read-only mount, but it succeeded")
	}
}

func TestFilterFSNegativePattern(t *testing.T) {
	checkFUSE(t)

	sourceDir := t.TempDir()
	mountPoint := t.TempDir()

	files := map[string]string{
		"a":    "content a",
		"b":    "content b",
		"keep": "content keep",
	}
	for relPath, content := range files {
		absPath := filepath.Join(sourceDir, relPath)
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Write ignorefile with wildcard ignore except keep.
	ignoreContent := "*\n!keep\n"
	ignoreFile := filepath.Join(sourceDir, ".gitignore")
	if err := os.WriteFile(ignoreFile, []byte(ignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	gitign, err := ignore.CompileIgnoreFile(ignoreFile)
	if err != nil {
		t.Fatal(err)
	}

	var gitignPtr atomic.Pointer[ignore.GitIgnore]
	gitignPtr.Store(gitign)

	var st syscall.Stat_t
	if err := syscall.Stat(sourceDir, &st); err != nil {
		t.Fatal(err)
	}

	root := &filterNode{
		gitign: &gitignPtr,
	}
	root.LoopbackNode.RootData = &fs.LoopbackRoot{
		NewNode: newFilterNode,
		Path:    sourceDir,
		Dev:     uint64(st.Dev),
	}
	root.LoopbackNode.RootData.RootNode = root

	opts := &fs.Options{}
	opts.Name = "filterfs"
	opts.FsName = "filterfs"
	opts.DirectMount = true
	opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	if err := server.WaitMount(); err != nil {
		t.Fatal(err)
	}

	// "keep" must be visible and readable.
	keepPath := filepath.Join(mountPoint, "keep")
	if _, err := os.Stat(keepPath); err != nil {
		t.Errorf("expected keep to be visible: %v", err)
	} else {
		data, err := os.ReadFile(keepPath)
		if err != nil {
			t.Errorf("expected to read keep: %v", err)
		} else if string(data) != "content keep" {
			t.Errorf("unexpected content of keep: %s", string(data))
		}
	}

	// "a" and "b" must NOT be visible.
	aPath := filepath.Join(mountPoint, "a")
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Errorf("expected a to be ignored, got: %v", err)
	}
	bPath := filepath.Join(mountPoint, "b")
	if _, err := os.Stat(bPath); !os.IsNotExist(err) {
		t.Errorf("expected b to be ignored, got: %v", err)
	}

	// Readdir must only list "keep".
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, entry := range entries {
		listed = append(listed, entry.Name())
	}
	// We expect listed to be exactly ["keep"].
	if len(listed) != 1 || listed[0] != "keep" {
		t.Errorf("expected only 'keep' to be listed, got: %v", listed)
	}
}

func TestFilterFSHotReload(t *testing.T) {
	checkFUSE(t)

	sourceDir := t.TempDir()
	mountPoint := t.TempDir()

	files := map[string]string{
		"a": "content a",
		"b": "content b",
	}
	for relPath, content := range files {
		absPath := filepath.Join(sourceDir, relPath)
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ignoreFile := filepath.Join(sourceDir, ".gitignore")
	if err := os.WriteFile(ignoreFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	gitign, err := ignore.CompileIgnoreFile(ignoreFile)
	if err != nil {
		t.Fatal(err)
	}

	var gitignPtr atomic.Pointer[ignore.GitIgnore]
	gitignPtr.Store(gitign)

	watchIgnoreFile(ignoreFile, &gitignPtr)

	var st syscall.Stat_t
	if err := syscall.Stat(sourceDir, &st); err != nil {
		t.Fatal(err)
	}

	root := &filterNode{
		gitign: &gitignPtr,
	}
	root.LoopbackNode.RootData = &fs.LoopbackRoot{
		NewNode: newFilterNode,
		Path:    sourceDir,
		Dev:     uint64(st.Dev),
	}
	root.LoopbackNode.RootData.RootNode = root

	opts := &fs.Options{}
	opts.Name = "filterfs"
	opts.FsName = "filterfs"
	opts.DirectMount = true
	opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	if err := server.WaitMount(); err != nil {
		t.Fatal(err)
	}

	// Initially both a and b are visible.
	aPath := filepath.Join(mountPoint, "a")
	bPath := filepath.Join(mountPoint, "b")
	if _, err := os.Stat(aPath); err != nil {
		t.Errorf("initially expected a to be visible: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Errorf("initially expected b to be visible: %v", err)
	}

	// Change ignorefile: ignore "a".
	if err := os.WriteFile(ignoreFile, []byte("/a\n"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Now "a" should be invisible (ENOENT) and "b" should still be visible.
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Errorf("after reloading, expected a to be ignored (not found), got: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Errorf("after reloading, expected b to still be visible: %v", err)
	}

	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, entry := range entries {
		if entry.Name() != ".gitignore" {
			listed = append(listed, entry.Name())
		}
	}
	if len(listed) != 1 || listed[0] != "b" {
		t.Errorf("after reloading, expected only 'b' to be listed, got: %v", listed)
	}
}

func TestFilterFSWrites(t *testing.T) {
	checkFUSE(t)

	sourceDir := t.TempDir()
	mountPoint := t.TempDir()

	files := map[string]string{
		"a": "initial content a",
		"b": "initial content b",
	}
	for relPath, content := range files {
		absPath := filepath.Join(sourceDir, relPath)
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ignoreFile := filepath.Join(sourceDir, ".gitignore")
	if err := os.WriteFile(ignoreFile, []byte("/a\n"), 0644); err != nil {
		t.Fatal(err)
	}

	gitign, err := ignore.CompileIgnoreFile(ignoreFile)
	if err != nil {
		t.Fatal(err)
	}

	var gitignPtr atomic.Pointer[ignore.GitIgnore]
	gitignPtr.Store(gitign)

	var st syscall.Stat_t
	if err := syscall.Stat(sourceDir, &st); err != nil {
		t.Fatal(err)
	}

	root := &filterNode{
		gitign: &gitignPtr,
	}
	root.LoopbackNode.RootData = &fs.LoopbackRoot{
		NewNode: newFilterNode,
		Path:    sourceDir,
		Dev:     uint64(st.Dev),
	}
	root.LoopbackNode.RootData.RootNode = root

	opts := &fs.Options{}
	opts.Name = "filterfs"
	opts.FsName = "filterfs"
	opts.DirectMount = true // Read-write by default.
	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	if err := server.WaitMount(); err != nil {
		t.Fatal(err)
	}

	// Check existing visible file modification and propagation.
	bPath := filepath.Join(mountPoint, "b")
	updatedContent := "updated content b!"
	if err := os.WriteFile(bPath, []byte(updatedContent), 0644); err != nil {
		t.Fatalf("failed to write to visible file: %v", err)
	}

	// Read directly from backing source directory to confirm propagation.
	backingBPath := filepath.Join(sourceDir, "b")
	data, err := os.ReadFile(backingBPath)
	if err != nil {
		t.Fatalf("failed to read from backing file: %v", err)
	}
	if string(data) != updatedContent {
		t.Errorf("expected propagated content %q, got %q", updatedContent, string(data))
	}

	// Check creating a new visible file and propagation.
	newFilePath := filepath.Join(mountPoint, "new_visible.txt")
	newFileContent := "hello world from FUSE mount!"
	if err := os.WriteFile(newFilePath, []byte(newFileContent), 0644); err != nil {
		t.Fatalf("failed to create new visible file: %v", err)
	}

	// Read directly from backing source directory to confirm propagation.
	backingNewPath := filepath.Join(sourceDir, "new_visible.txt")
	data, err = os.ReadFile(backingNewPath)
	if err != nil {
		t.Fatalf("failed to read new file from backing dir: %v", err)
	}
	if string(data) != newFileContent {
		t.Errorf("expected propagated content for new file %q, got %q", newFileContent, string(data))
	}

	// Check blocking of ignored file creation.
	newIgnoredPath := filepath.Join(mountPoint, "a") // "a" matches "/a" in ignore rules
	err = os.WriteFile(newIgnoredPath, []byte("should fail"), 0644)
	if err == nil {
		t.Errorf("expected writing to ignored file 'a' to fail, but it succeeded")
	} else if !os.IsPermission(err) {
		t.Errorf("expected permission error (EPERM), got: %v", err)
	}
}
