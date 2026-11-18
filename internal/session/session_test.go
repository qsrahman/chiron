package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func file(id, dir string, updated time.Time) File {
	return File{ID: id, Dir: dir, Model: "m", Updated: updated, Items: []Item{{Kind: "user", Raw: []byte(`{"content":"hi","role":"user"}`)}}}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	want := file("20261118-100000-abcd", "/work", time.Now())
	want.Summary = "## Goal"
	want.Items = append(want.Items, Item{Kind: "function_call_output", CallID: "c1", Raw: []byte(`{"output":"x","call_id":"c1"}`)})
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != "/work" || got.Summary != "## Goal" || len(got.Items) != 2 || got.Items[1].CallID != "c1" || string(got.Items[0].Raw) != string(want.Items[0].Raw) {
		t.Errorf("got %+v", got)
	}
}

func TestFilesAndDirectoryArePrivate(t *testing.T) {
	state := t.TempDir()
	s := NewStore(state)
	if err := s.Save(file("a-1", "/w", time.Now())); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Stat(filepath.Join(state, "sessions"))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Errorf("directory: %v %v", dir, err)
	}
	f, err := os.Stat(filepath.Join(state, "sessions", "a-1.json"))
	if err != nil || f.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", f, err)
	}
}

func TestAFailedSaveKeepsThePreviousFileAndLeavesNoTemporaryFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	state := t.TempDir()
	s := NewStore(state)
	if err := s.Save(file("a-1", "/w", time.Now())); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "sessions")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	changed := file("a-1", "/other", time.Now())
	if err := s.Save(changed); err == nil {
		t.Fatal("saving into a read-only directory must fail")
	}
	got, err := s.Load("a-1")
	if err != nil || got.Dir != "/w" {
		t.Errorf("the previous file was lost: %+v, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%d files in the directory, want 1", len(entries))
	}
}

func TestLoadByPrefix(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"20261118-1", "20261118-12", "20261118-2", "20261119-1"} {
		if err := s.Save(file(id, "/w", time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if f, err := s.Load("20261119"); err != nil || f.ID != "20261119-1" {
		t.Errorf("unique prefix: %v %v", f.ID, err)
	}
	if _, err := s.Load("20261118"); err == nil || !strings.Contains(err.Error(), "matches 3") {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if f, err := s.Load("20261118-1"); err != nil || f.ID != "20261118-1" {
		t.Errorf("an exact name wins over longer ones: %v %v", f.ID, err)
	}
	if _, err := s.Load("nope"); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Errorf("unknown: %v", err)
	}
}

func TestAnIdThatIsNotAnIdIsRefused(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"../x", "a/b", "A", "", "a.json"} {
		if _, err := s.Load(id); err == nil {
			t.Errorf("Load(%q) must fail", id)
		}
		if err := s.Save(File{ID: id}); err == nil {
			t.Errorf("Save(%q) must fail", id)
		}
	}
}

func TestListIsNewestFirstAndSkipsDamagedFiles(t *testing.T) {
	state := t.TempDir()
	s := NewStore(state)
	now := time.Now()
	s.Save(file("old-1", "/w", now.Add(-time.Hour)))
	s.Save(file("new-1", "/w", now))
	os.WriteFile(filepath.Join(state, "sessions", "bad-1.json"), []byte("{not json"), 0o600)
	os.WriteFile(filepath.Join(state, "sessions", "v9-1.json"), []byte(`{"version":9,"id":"v9-1"}`), 0o600)

	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].ID != "new-1" || list[1].ID != "old-1" {
		t.Errorf("list = %v, %v", list, err)
	}
	if _, err := s.Load("bad-1"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a damaged file should say so: %v", err)
	}
	if _, err := s.Load("v9-1"); err == nil || !strings.Contains(err.Error(), "version 9") {
		t.Errorf("a file of another version should say so: %v", err)
	}
}

func TestLatestIsPerDirectory(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Now()
	s.Save(file("a-1", "/one", now.Add(-time.Hour)))
	s.Save(file("b-1", "/two", now))
	if f, err := s.Latest("/one"); err != nil || f.ID != "a-1" {
		t.Errorf("latest = %v %v", f.ID, err)
	}
	if _, err := s.Latest("/three"); err == nil {
		t.Error("a directory with no session must be an error")
	}
}

func TestNoSessionsYetIsNotAnError(t *testing.T) {
	list, err := NewStore(t.TempDir()).List()
	if err != nil || len(list) != 0 {
		t.Errorf("%v %v", list, err)
	}
}

func TestNewIDSortsByTime(t *testing.T) {
	a := NewID(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	b := NewID(time.Date(2026, 11, 18, 10, 0, 1, 0, time.UTC))
	if !(a < b) || !validID.MatchString(a) {
		t.Errorf("%s %s", a, b)
	}
}
