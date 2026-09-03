package harness

import "testing"

func TestSharedYoloAndFlagHelpers(t *testing.T) {
	got := PrependOnce([]string{"chat"}, "--force", "--yolo")
	if got[0] != "--force" {
		t.Fatalf("got=%q", got)
	}
	same := PrependOnce([]string{"--yolo", "chat"}, "--force", "--yolo")
	if len(same) != 2 {
		t.Fatalf("duplicated=%q", same)
	}
	if !HasFlagValue([]string{"--cwd=/tmp"}, "--cwd") || !HasFlagValue([]string{"--cwd", "/tmp"}, "--cwd") {
		t.Fatal("flag value not detected")
	}
}
