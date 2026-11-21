package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsCheckout(t *testing.T) {
	// This package's test runs inside the real checkout.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !isCheckout(root) {
		t.Errorf("%s should count as Draftsmith's source tree", root)
	}

	// A folder with just the exe (a copy somewhere else) isn't one.
	dir := t.TempDir()
	if isCheckout(dir) {
		t.Error("an empty folder isn't a checkout")
	}

	// Nor is another module that happens to have cmd/draftsmith.
	os.MkdirAll(filepath.Join(dir, "cmd", "draftsmith"), 0o755)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n\ngo 1.27\n"), 0o644)
	if isCheckout(dir) {
		t.Error("another module isn't Draftsmith's checkout")
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// comment\nmodule "+modulePath+"\n"), 0o644)
	if !isCheckout(dir) {
		t.Error("Draftsmith's go.mod with cmd/draftsmith is a checkout")
	}
}
