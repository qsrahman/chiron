// Package session saves conversations as files and reads them back. It knows nothing about what the items mean:
// the agent turns its history into Items and back.
package session

import (
	"cmp"
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
	Title    string    `json:"title,omitempty"`    // the first prompt, for lists: the items lose it when a summary replaces it
	Turns    int       `json:"turns,omitempty"`    // all the turns of the conversation, those a summary stands for included
	Replaced int       `json:"replaced,omitempty"` // turns a summary stands for, which are no longer in Items
	Items    []Item    `json:"items"`
}

// Info is what a list shows of a session, read without its items.
type Info struct {
	ID      string
	Dir     string
	Updated time.Time
	Title   string
	Turns   int
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
// file in the same directory and renamed over it. The directory is private to the user, and so are the files. It
// returns the modification time of the file it wrote, to notice a later write by someone else (see Stamp).
func (s *Store) Save(f File) (time.Time, error) {
	if !validID.MatchString(f.ID) {
		return time.Time{}, fmt.Errorf("bad session id %q", f.ID)
	}
	f.Version = version
	data, err := json.Marshal(f)
	if err != nil {
		return time.Time{}, fmt.Errorf("encoding the session: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return time.Time{}, fmt.Errorf("creating %s: %w", s.dir, err)
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*") // created with mode 0600
	if err != nil {
		return time.Time{}, fmt.Errorf("writing the session: %w", err)
	}
	defer os.Remove(tmp.Name()) // does nothing after a successful rename
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("writing the session: %w", err)
	}
	path := filepath.Join(s.dir, f.ID+".json")
	if err := os.Rename(tmp.Name(), path); err != nil {
		return time.Time{}, fmt.Errorf("writing the session: %w", err)
	}
	return s.Stamp(f.ID)
}

// Stamp is the modification time of the session's file, zero if there is none. A writer that remembers the stamp of
// its last Save can tell that another process has written the file since.
func (s *Store) Stamp(id string) (time.Time, error) {
	info, err := os.Stat(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("reading session %s: %w", id, err)
	}
	return info.ModTime(), nil
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

// List returns the sessions that match, the most recently written first, at most limit of them (0 means all). The
// items of a file are not kept in memory. A file that cannot be read is left out: one damaged file must not hide the
// others.
func (s *Store) List(match func(Info) bool, limit int) ([]Info, error) {
	names, err := s.names()
	if err != nil {
		return nil, err
	}
	var list []Info
	for _, n := range names {
		info, err := s.readInfo(n)
		if err != nil || !match(info) {
			continue
		}
		list = append(list, info)
		if limit > 0 && len(list) == limit {
			break
		}
	}
	return list, nil
}

// Latest returns the session written last that began in dir. The files it had to pass over because they are damaged
// are returned as warnings: one of them may have been the session the user means.
func (s *Store) Latest(dir string) (f File, warnings []string, err error) {
	names, err := s.names()
	if err != nil {
		return File{}, nil, err
	}
	for _, n := range names {
		info, err := s.readInfo(n)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if info.Dir != dir {
			continue
		}
		f, err := s.read(n)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		return f, warnings, nil
	}
	return File{}, warnings, fmt.Errorf("no saved session for %s", dir)
}

// names lists the session ids, the most recently written first; a missing directory means there are none.
func (s *Store) names() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.dir, err)
	}
	type named struct {
		id  string
		mod time.Time
	}
	var found []named
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !validID.MatchString(id) {
			continue
		}
		if info, err := e.Info(); err == nil {
			found = append(found, named{id, info.ModTime()})
		}
	}
	slices.SortFunc(found, func(a, b named) int { return cmp.Or(b.mod.Compare(a.mod), strings.Compare(b.id, a.id)) })
	names := make([]string, len(found))
	for i, n := range found {
		names[i] = n.id
	}
	return names, nil
}

func (s *Store) read(id string) (f File, err error) {
	if err := s.decode(id, &f); err != nil {
		return File{}, err
	}
	return f, s.check(id, f.Version, f.ID)
}

// readInfo reads only what a list shows: decoding skips the items without keeping them.
func (s *Store) readInfo(id string) (Info, error) {
	var h struct {
		Version int       `json:"version"`
		ID      string    `json:"id"`
		Dir     string    `json:"dir"`
		Updated time.Time `json:"updated"`
		Title   string    `json:"title"`
		Turns   int       `json:"turns"`
	}
	if err := s.decode(id, &h); err != nil {
		return Info{}, err
	}
	if err := s.check(id, h.Version, h.ID); err != nil {
		return Info{}, err
	}
	return Info{ID: h.ID, Dir: h.Dir, Updated: h.Updated, Title: h.Title, Turns: h.Turns}, nil
}

func (s *Store) decode(id string, v any) error {
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading session %s: %w", id, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("session %s is damaged (%s): %w", id, path, err)
	}
	return nil
}

func (s *Store) check(id string, fileVersion int, fileID string) error {
	if fileVersion != version {
		return fmt.Errorf("session %s has format version %d; this program reads version %d", id, fileVersion, version)
	}
	if fileID != id {
		return fmt.Errorf("session %s is damaged: it says it is %q", id, fileID)
	}
	return nil
}
