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
	if _, err := s.Save(want); err != nil {
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
	if _, err := s.Save(file("a-1", "/w", time.Now())); err != nil {
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
	if _, err := s.Save(file("a-1", "/w", time.Now())); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "sessions")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	changed := file("a-1", "/other", time.Now())
	if _, err := s.Save(changed); err == nil {
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
		if _, err := s.Save(file(id, "/w", time.Now())); err != nil {
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
		if _, err := s.Save(File{ID: id}); err == nil {
			t.Errorf("Save(%q) must fail", id)
		}
	}
}

// saveAt saves a session and sets the modification time of its file, which is what orders sessions.
func saveAt(t *testing.T, s *Store, f File, mod time.Time) {
	t.Helper()
	if _, err := s.Save(f); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(s.dir, f.ID+".json"), mod, mod); err != nil {
		t.Fatal(err)
	}
}

func all(Info) bool { return true }

func TestListIsNewestFirstLimitedAndSkipsDamagedFiles(t *testing.T) {
	state := t.TempDir()
	s := NewStore(state)
	now := time.Now()
	saveAt(t, s, file("old-1", "/w", now), now.Add(-2*time.Hour))
	saveAt(t, s, file("mid-1", "/w", now), now.Add(-time.Hour))
	saveAt(t, s, file("new-1", "/w", now), now)
	os.WriteFile(filepath.Join(state, "sessions", "bad-1.json"), []byte("{not json"), 0o600)
	os.WriteFile(filepath.Join(state, "sessions", "v9-1.json"), []byte(`{"version":9,"id":"v9-1"}`), 0o600)

	list, err := s.List(all, 0)
	if err != nil || len(list) != 3 || list[0].ID != "new-1" || list[1].ID != "mid-1" || list[2].ID != "old-1" {
		t.Errorf("list = %v, %v", list, err)
	}
	if list, _ := s.List(all, 2); len(list) != 2 || list[1].ID != "mid-1" {
		t.Errorf("limit 2: %v", list)
	}
	if _, err := s.Load("bad-1"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a damaged file should say so: %v", err)
	}
	if _, err := s.Load("v9-1"); err == nil || !strings.Contains(err.Error(), "version 9") {
		t.Errorf("a file of another version should say so: %v", err)
	}
}

func TestListReadsTheHeaderAndFiltersByDirectory(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Now()
	a := file("a-1", "/one", now)
	a.Title, a.Turns = "the first prompt", 7
	saveAt(t, s, a, now)
	saveAt(t, s, file("b-1", "/two", now), now.Add(-time.Minute))

	list, _ := s.List(func(i Info) bool { return i.Dir == "/one" }, 0)
	if len(list) != 1 || list[0].Title != "the first prompt" || list[0].Turns != 7 {
		t.Errorf("list = %+v", list)
	}
}

func TestLatestIsPerDirectoryAndWarnsAboutDamagedFilesItPassed(t *testing.T) {
	state := t.TempDir()
	s := NewStore(state)
	now := time.Now()
	saveAt(t, s, file("a-1", "/one", now), now.Add(-time.Hour))
	saveAt(t, s, file("b-1", "/two", now), now.Add(-time.Minute))
	bad := filepath.Join(state, "sessions", "bad-1.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	os.Chtimes(bad, now, now) // the newest file of all is the damaged one

	f, warnings, err := s.Latest("/one")
	if err != nil || f.ID != "a-1" {
		t.Fatalf("latest = %v %v", f.ID, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bad-1") {
		t.Errorf("warnings = %q: the damaged newer file may have been the one meant", warnings)
	}
	if _, _, err := s.Latest("/three"); err == nil {
		t.Error("a directory with no session must be an error")
	}
}

func TestStampTellsWhenAnotherWriterWasHere(t *testing.T) {
	s := NewStore(t.TempDir())
	stamp, err := s.Save(file("a-1", "/w", time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Stamp("a-1"); !again.Equal(stamp) {
		t.Error("the stamp changed without a write")
	}
	later := stamp.Add(time.Second)
	os.Chtimes(filepath.Join(s.dir, "a-1.json"), later, later) // someone else wrote it
	if again, _ := s.Stamp("a-1"); again.Equal(stamp) {
		t.Error("the stamp did not change")
	}
	if zero, err := s.Stamp("none-1"); err != nil || !zero.IsZero() {
		t.Errorf("a missing file has the zero stamp: %v %v", zero, err)
	}
}

func TestNoSessionsYetIsNotAnError(t *testing.T) {
	list, err := NewStore(t.TempDir()).List(all, 0)
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

func TestDeleteRemovesOnlyTheNamedSession(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"a-1", "a-10"} {
		if _, err := s.Save(file(id, "/w", time.Now())); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Delete("a-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("a-10"); err != nil {
		t.Errorf("a longer id that starts the same way was deleted too: %v", err)
	}
	if list, _ := s.List(func(Info) bool { return true }, 0); len(list) != 1 || list[0].ID != "a-10" {
		t.Errorf("list = %+v", list)
	}
	if err := s.Delete("a-1"); err != nil {
		t.Errorf("deleting a session that is already gone is not an error, got %v", err)
	}
	if err := s.Delete("../x"); err == nil || !strings.Contains(err.Error(), "not a session id") {
		t.Errorf("an id that is not an id: %v", err)
	}
}
