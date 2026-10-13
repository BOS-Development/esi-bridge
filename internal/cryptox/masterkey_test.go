package cryptox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMasterKeyGeneratesThenReloadsSameKey(t *testing.T) {
	dir := t.TempDir()

	k1, err := MasterKey(dir)
	if err != nil {
		t.Fatalf("first MasterKey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.key")); err != nil {
		t.Errorf("expected master.key to be written: %v", err)
	}

	k2, err := MasterKey(dir)
	if err != nil {
		t.Fatalf("second MasterKey: %v", err)
	}
	if *k1 != *k2 {
		t.Error("reloaded master key differs from the generated one")
	}
}

func TestMasterKeyRejectsWrongLength(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "master.key"), []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MasterKey(dir); err == nil {
		t.Fatal("expected MasterKey to reject a key file of the wrong length")
	}
}
