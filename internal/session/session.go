// Package session saves conversations as files and reads them back. It knows nothing about what the items mean:
// the agent turns its history into Items and back.
package session

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// version is the file format. A file of another version is refused, not guessed at.
const version = 1

// Item is one entry of the conversation. Raw is the JSON the API takes for it, replayed as it is.
type Item struct {
	Kind   string          `json:"kind"`
	CallID string          `json:"call_id,omitempty"`
	Raw    json.RawMessage `json:"raw"`
}

// File is one saved conversation.
type File struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Dir      string    `json:"dir"`      // the working directory the conversation started in
	Model    string    `json:"model"`    // reasoning items only replay to the model that wrote them
	Endpoint string    `json:"endpoint"` // scheme and host of the server, for the same reason
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Summary  string    `json:"summary,omitempty"`
	Items    []Item    `json:"items"`
}

// Store keeps sessions in one directory, a file each.
type Store struct{ dir string }

// NewStore returns a store under stateDir/sessions. Nothing is created until the first Save.
func NewStore(stateDir string) *Store { return &Store{dir: filepath.Join(stateDir, "sessions")} }

var validID = regexp.MustCompile(`^[0-9a-z-]+$`)

// NewID returns a name that sorts by time and does not clash: 20261118-153012-k3f9.
func NewID(now time.Time) string {
	return now.Format("20060102-150405") + "-" + strings.ToLower(rand.Text()[:4])
}

// Save writes the file so that a failure never loses the previous one: the new content is written to a temporary
// file in the same directory and renamed over it. The directory is private to the user, and so are the files.
func (s *Store) Save(f File) error {
	if !validID.MatchString(f.ID) {
		return fmt.Errorf("bad session id %q", f.ID)
	}
	f.Version = version
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encoding the session: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", s.dir, err)
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*") // created with mode 0600
	if err != nil {
		return fmt.Errorf("writing the session: %w", err)
	}
	defer os.Remove(tmp.Name()) // does nothing after a successful rename
	if _, err := tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing the session: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, f.ID+".json")); err != nil {
		return fmt.Errorf("writing the session: %w", err)
	}
	return nil
}

// Load reads the session named id, or the only one whose name starts with it.
func (s *Store) Load(id string) (File, error) {
	if !validID.MatchString(id) {
		return File{}, fmt.Errorf("%q is not a session id", id)
	}
	names, err := s.names()
	if err != nil {
		return File{}, err
	}
	var found []string
	for _, n := range names {
		if n == id {
			found = []string{n}
			break
		}
		if strings.HasPrefix(n, id) {
			found = append(found, n)
		}
	}
	switch len(found) {
	case 0:
		return File{}, fmt.Errorf("no session %q (/sessions lists them)", id)
	case 1:
		return s.read(found[0])
	}
	return File{}, fmt.Errorf("%q matches %d sessions: give more of the name", id, len(found))
}

// List returns the readable sessions, the newest first. A file that cannot be read is left out: one damaged file
// must not hide the others.
func (s *Store) List() ([]File, error) {
	names, err := s.names()
	if err != nil {
		return nil, err
	}
	var files []File
	for _, n := range names {
		if f, err := s.read(n); err == nil {
			files = append(files, f)
		}
	}
	slices.SortFunc(files, func(a, b File) int { return b.Updated.Compare(a.Updated) })
	return files, nil
}

// Latest returns the newest session that began in dir.
func (s *Store) Latest(dir string) (File, error) {
	files, err := s.List()
	if err != nil {
		return File{}, err
	}
	for _, f := range files {
		if f.Dir == dir {
			return f, nil
		}
	}
	return File{}, fmt.Errorf("no saved session for %s", dir)
}

// names lists the session ids; a missing directory means there are none.
func (s *Store) names() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.dir, err)
	}
	var names []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && validID.MatchString(id) {
			names = append(names, id)
		}
	}
	return names, nil
}

func (s *Store) read(id string) (File, error) {
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("reading session %s: %w", id, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("session %s is damaged (%s): %w", id, path, err)
	}
	if f.Version != version {
		return File{}, fmt.Errorf("session %s has format version %d; this program reads version %d", id, f.Version, version)
	}
	if f.ID != id {
		return File{}, fmt.Errorf("session %s is damaged: it says it is %q", id, f.ID)
	}
	return f, nil
}
