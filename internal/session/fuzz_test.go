package session

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad gives the store a damaged or hostile file: it must be refused or read, never crash the program.
func FuzzLoad(f *testing.F) {
	for _, s := range []string{
		"", "{}", "null", "[]", `{"version":1,"id":"x-1"}`, `{"version":9,"id":"x-1"}`, `{"version":1,"id":"other"}`,
		`{"version":1,"id":"x-1","dir":"/d","items":[{"kind":"user","raw":null}]}`,
		`{"version":1,"id":"x-1","updated":"never"}`, `{"version":1,"id":"x-1","items":{}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		store := NewStore(t.TempDir())
		if err := os.MkdirAll(store.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.dir, "x-1.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}

		file, err := store.Load("x-1")
		if _, listErr := store.List(func(Info) bool { return true }, 0); listErr != nil {
			t.Errorf("one file broke the list: %v", listErr)
		}
		_, _, _ = store.Latest("/d")
		if err != nil {
			return
		}
		if file.ID != "x-1" || file.Version != version {
			t.Errorf("accepted a file with id %q, version %d", file.ID, file.Version)
		}
		// What can be read can be written again, and read back.
		if _, err := store.Save(file); err != nil {
			t.Fatalf("cannot save what was loaded: %v", err)
		}
		if _, err := store.Load("x-1"); err != nil {
			t.Errorf("cannot load what was saved: %v", err)
		}
	})
}
