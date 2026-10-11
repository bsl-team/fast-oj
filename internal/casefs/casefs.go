package casefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Store keeps case payloads on disk and only their paths in SQLite, so the
// database stays small enough to copy around.
type Store struct{ root string }

func New(dataDir string) *Store { return &Store{root: filepath.Join(dataDir, "cases")} }

func (s *Store) Root() string { return s.root }

func (s *Store) problemDir(problemID int64) string {
	return filepath.Join(s.root, strconv.FormatInt(problemID, 10))
}

// Put writes one case and returns its paths relative to the store root.
func (s *Store) Put(problemID int64, ordinal int, input, output []byte) (string, string, error) {
	dir := s.problemDir(problemID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", err
	}
	relIn := fmt.Sprintf("%d/%d.in", problemID, ordinal)
	relOut := fmt.Sprintf("%d/%d.out", problemID, ordinal)
	if err := s.write(relIn, input); err != nil {
		return "", "", err
	}
	if err := s.write(relOut, output); err != nil {
		return "", "", err
	}
	return relIn, relOut, nil
}

func (s *Store) write(rel string, data []byte) error {
	abs, err := s.abs(rel)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o640)
}

func (s *Store) abs(rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(rel, "/")))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe case path %q", rel)
	}
	return filepath.Join(s.root, clean), nil
}

func (s *Store) Read(rel string) ([]byte, error) {
	abs, err := s.abs(rel)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(abs)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: case file %s", os.ErrNotExist, rel)
	}
	return body, err
}

func (s *Store) Size(rel string) (int64, error) {
	abs, err := s.abs(rel)
	if err != nil {
		return -1, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return -1, err
	}
	return info.Size(), nil
}

func (s *Store) RemoveProblem(problemID int64) error {
	return os.RemoveAll(s.problemDir(problemID))
}
