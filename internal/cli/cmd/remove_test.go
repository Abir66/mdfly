package cmd

import (
	"path/filepath"
	"testing"

	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
)

func tokenExists(t *testing.T, dir, slug string) bool {
	t.Helper()
	_, ok, err := localstate.New(dir).LoadToken(slug)
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	return ok
}

func TestRemove_bySlug_prunesLocalOnly_noNetwork(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", nil)
	m := noNetMock(t)

	out, _, err := executeIn(t, dir, "--api", m.URL, "remove", "s1")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if out != "Removed: s1\n" {
		t.Errorf("stdout=%q, want Removed: s1", out)
	}
	if recordExists(t, dir, "s1") {
		t.Error("record must be pruned")
	}
	if tokenExists(t, dir, "s1") {
		t.Error("edit token must be pruned")
	}
}

func TestRemove_byFile_disambiguation(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "notes.md")
	seedRecord(t, dir, "s1", "mftk_a", "mh1", &abs)
	seedRecord(t, dir, "s2", "mftk_b", "mh2", &abs)
	m := noNetMock(t)

	_, _, err := executeIn(t, dir, "--api", m.URL, "remove", abs)
	if err == nil {
		t.Fatal("ambiguous file should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want usage", output.ExitCode(err))
	}

	if _, _, err := executeIn(t, dir, "--api", m.URL, "remove", abs, "--slug", "s2"); err != nil {
		t.Fatalf("remove --slug s2: %v", err)
	}
	if recordExists(t, dir, "s2") {
		t.Error("s2 must be pruned")
	}
	if !recordExists(t, dir, "s1") {
		t.Error("s1 must be untouched")
	}
}

func TestRemove_all_wipesLedger_yesSkipsPrompt(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_a", "mh1", nil)
	seedRecord(t, dir, "s2", "mftk_b", "mh2", nil)
	m := noNetMock(t)

	if _, _, err := executeIn(t, dir, "--api", m.URL, "remove", "--all", "-y"); err != nil {
		t.Fatalf("remove --all -y: %v", err)
	}
	if recordExists(t, dir, "s1") || recordExists(t, dir, "s2") {
		t.Error("--all must wipe the whole ledger")
	}
	if tokenExists(t, dir, "s1") || tokenExists(t, dir, "s2") {
		t.Error("--all must wipe credentials")
	}
}

func TestRemove_allNonTTYWithoutYesRefuses(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_a", "mh1", nil)
	m := noNetMock(t)

	_, _, err := executeIn(t, dir, "--api", m.URL, "remove", "--all")
	if err == nil {
		t.Fatal("non-TTY --all without -y should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want usage", output.ExitCode(err))
	}
	if !recordExists(t, dir, "s1") {
		t.Error("refused --all must not wipe the ledger")
	}
}

func TestRemove_unknownSlug_usageError(t *testing.T) {
	dir := t.TempDir()
	m := noNetMock(t)

	_, _, err := executeIn(t, dir, "--api", m.URL, "remove", "ghost")
	if err == nil {
		t.Fatal("removing an untracked slug should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want usage", output.ExitCode(err))
	}
}
