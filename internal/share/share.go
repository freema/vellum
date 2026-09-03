// Package share implements public, read-only links to individual notes.
//
// A share is a capability, not a copy: a random token bound to a vault path.
// Whoever holds the link reads the *current* note, so an edit shows up
// immediately and a revoke takes effect immediately — the same "one URL, live
// content" model the workspace already has for its own deep links.
//
// State lives in a single JSON file inside the vault, at
// `.vellum/shares.json`. That location is deliberate:
//
//   - It survives a restart. vellum's OAuth tokens are in memory and die with
//     the process by design; a link handed to somebody else must not.
//   - It travels with the vault, which is backed up by copying a directory.
//     No database, no second thing to restore.
//   - It is unreachable from a note path. The vault refuses writes into
//     dot-directories and the index skips them, so an agent holding an MCP
//     token cannot publish a note by writing to this file or by setting some
//     `share:` key in frontmatter — sharing only ever happens through the
//     explicit calls in this package.
package share

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// StateDir is the vault-relative directory holding vellum's own state.
	StateDir = ".vellum"
	fileName = "shares.json"

	// MinTTL and MaxTTL bound a caller-supplied lifetime. A zero TTL means
	// "until revoked" and is always allowed.
	MinTTL = time.Hour
	MaxTTL = 365 * 24 * time.Hour

	// tokenBytes is the entropy behind a link. 24 bytes = 192 bits, so the
	// token is the only secret needed and guessing one is not a threat.
	tokenBytes = 24

	// flushInterval is how often view counters are written back. Minting and
	// revoking persist synchronously; counting a view is not worth an fsync
	// per request.
	flushInterval = 30 * time.Second
)

// ErrInvalidTTL is returned for a lifetime outside [MinTTL, MaxTTL].
var ErrInvalidTTL = errors.New("share lifetime must be between 1 hour and 365 days")

// Share is one public link.
type Share struct {
	Token      string     `json:"token"`
	Path       string     `json:"path"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Views      int        `json:"views"`
	LastViewAt *time.Time `json:"lastViewAt,omitempty"`
}

// Expired reports whether the share has passed its expiry.
func (s Share) Expired(now time.Time) bool {
	return s.ExpiresAt != nil && !now.Before(*s.ExpiresAt)
}

// Store is the set of live shares, persisted to `.vellum/shares.json`.
// Safe for concurrent use.
type Store struct {
	file string

	mu      sync.Mutex
	byToken map[string]*Share
	byPath  map[string]*Share
	dirty   bool

	now  func() time.Time
	stop chan struct{}
	wg   sync.WaitGroup
}

type fileFormat struct {
	Version int      `json:"version"`
	Shares  []*Share `json:"shares"`
}

// Open loads (or starts) the share store for a vault root. It creates the
// `.vellum` directory, so call it only when sharing is enabled — an
// installation with sharing off should not grow a state directory.
func Open(vaultRoot string) (*Store, error) {
	dir := filepath.Join(vaultRoot, StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	s := &Store{
		file:    filepath.Join(dir, fileName),
		byToken: map[string]*Share{},
		byPath:  map[string]*Share{},
		now:     time.Now,
		stop:    make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go s.flushLoop()
	return s, nil
}

// load reads the JSON file. A missing file is an empty store; a corrupt one
// is an error, because silently starting with zero shares would break every
// link that was already handed out without saying so.
func (s *Store) load() error {
	data, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.file, err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse %s: %w", s.file, err)
	}
	now := s.now()
	for _, sh := range f.Shares {
		if sh == nil || sh.Token == "" || sh.Path == "" || sh.Expired(now) {
			continue
		}
		s.byToken[sh.Token] = sh
		s.byPath[sh.Path] = sh
	}
	return nil
}

// Create mints the link for path, or refreshes the one it already has.
// Re-sharing a note keeps its existing token — the URL somebody was given
// stays valid — and only moves the expiry. A ttl of 0 means "until revoked".
func (s *Store) Create(path string, ttl time.Duration) (Share, error) {
	if ttl != 0 && (ttl < MinTTL || ttl > MaxTTL) {
		return Share{}, ErrInvalidTTL
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return Share{}, errors.New("empty note path")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)

	var expires *time.Time
	if ttl != 0 {
		t := now.Add(ttl)
		expires = &t
	}

	if existing, ok := s.byPath[path]; ok {
		existing.ExpiresAt = expires
		if err := s.saveLocked(); err != nil {
			return Share{}, err
		}
		return *existing, nil
	}

	token, err := newToken()
	if err != nil {
		return Share{}, err
	}
	sh := &Share{Token: token, Path: path, CreatedAt: now, ExpiresAt: expires}
	s.byToken[token] = sh
	s.byPath[path] = sh
	if err := s.saveLocked(); err != nil {
		delete(s.byToken, token)
		delete(s.byPath, path)
		return Share{}, err
	}
	return *sh, nil
}

// Get resolves a token. An expired share reports false, exactly like an
// unknown one — the caller must not be able to tell them apart.
func (s *Store) Get(token string) (Share, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byToken[token]
	if !ok || sh.Expired(s.now()) {
		return Share{}, false
	}
	return *sh, true
}

// ForPath returns the live share of a note, if it has one.
func (s *Store) ForPath(path string) (Share, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byPath[strings.Trim(path, "/")]
	if !ok || sh.Expired(s.now()) {
		return Share{}, false
	}
	return *sh, true
}

// List returns every live share, newest first.
func (s *Store) List() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	out := make([]Share, 0, len(s.byToken))
	for _, sh := range s.byToken {
		out = append(out, *sh)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Len is the number of live shares.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	return len(s.byToken)
}

// Revoke kills one link by token.
func (s *Store) Revoke(token string) (Share, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byToken[token]
	if !ok {
		return Share{}, false
	}
	s.dropLocked(sh)
	_ = s.saveLocked()
	return *sh, true
}

// RevokePath kills the link of one note.
func (s *Store) RevokePath(path string) (Share, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byPath[strings.Trim(path, "/")]
	if !ok {
		return Share{}, false
	}
	s.dropLocked(sh)
	_ = s.saveLocked()
	return *sh, true
}

// RevokePrefix kills the links of every note inside a folder, for when the
// folder itself is deleted. Returns what it dropped.
func (s *Store) RevokePrefix(dir string) []Share {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return nil
	}
	prefix := dir + "/"
	s.mu.Lock()
	defer s.mu.Unlock()
	var dropped []Share
	for _, sh := range s.byPath {
		if strings.HasPrefix(sh.Path, prefix) {
			dropped = append(dropped, *sh)
		}
	}
	for i := range dropped {
		if sh, ok := s.byToken[dropped[i].Token]; ok {
			s.dropLocked(sh)
		}
	}
	if len(dropped) > 0 {
		_ = s.saveLocked()
	}
	return dropped
}

// Rename follows a note to its new path, so moving or renaming a shared note
// does not break the link — the same promise move_note makes for backlinks.
// A share already sitting on the destination is replaced.
func (s *Store) Rename(from, to string) bool {
	from, to = strings.Trim(from, "/"), strings.Trim(to, "/")
	if from == "" || to == "" || from == to {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byPath[from]
	if !ok {
		return false
	}
	if occupant, taken := s.byPath[to]; taken && occupant != sh {
		s.dropLocked(occupant)
	}
	delete(s.byPath, from)
	sh.Path = to
	s.byPath[to] = sh
	_ = s.saveLocked()
	return true
}

// RecordView counts a hit on a link. Cheap on purpose: the counter is kept in
// memory and written back by the flush loop.
func (s *Store) RecordView(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byToken[token]
	if !ok {
		return
	}
	now := s.now()
	sh.Views++
	sh.LastViewAt = &now
	s.dirty = true
}

// Close flushes pending view counts and stops the background writer.
func (s *Store) Close() error {
	select {
	case <-s.stop: // already closed
		return nil
	default:
		close(s.stop)
	}
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

func (s *Store) flushLoop() {
	defer s.wg.Done()
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			if s.dirty {
				_ = s.saveLocked()
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

// dropLocked removes a share from both indexes. Caller holds the lock.
func (s *Store) dropLocked(sh *Share) {
	delete(s.byToken, sh.Token)
	if cur, ok := s.byPath[sh.Path]; ok && cur == sh {
		delete(s.byPath, sh.Path)
	}
}

// pruneLocked forgets shares that have passed their expiry. Caller holds the
// lock. Expiry is enforced on every lookup regardless, so this only keeps the
// file from growing.
func (s *Store) pruneLocked(now time.Time) {
	var expired []*Share
	for _, sh := range s.byToken {
		if sh.Expired(now) {
			expired = append(expired, sh)
		}
	}
	for _, sh := range expired {
		s.dropLocked(sh)
	}
	if len(expired) > 0 {
		_ = s.saveLocked()
	}
}

// saveLocked writes the whole file atomically (temp file + rename), so a
// crash mid-write cannot leave a half-written share list behind. Caller holds
// the lock.
func (s *Store) saveLocked() error {
	list := make([]*Share, 0, len(s.byToken))
	for _, sh := range s.byToken {
		list = append(list, sh)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })

	data, err := json.MarshalIndent(fileFormat{Version: 1, Shares: list}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.file)
	tmp, err := os.CreateTemp(dir, ".shares-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", s.file, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", s.file, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", s.file, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", s.file, err)
	}
	if err := os.Rename(tmpName, s.file); err != nil {
		return fmt.Errorf("write %s: %w", s.file, err)
	}
	s.dirty = false
	return nil
}

// newToken returns a URL-safe random token.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenLen is the length of a minted token, used to reject obvious garbage
// before it reaches the store.
var TokenLen = base64.RawURLEncoding.EncodedLen(tokenBytes)

// PlausibleToken reports whether s could be a token vellum minted. It exists
// so the public endpoint can drop probe traffic early; a true result says
// nothing about the token existing.
func PlausibleToken(s string) bool {
	if len(s) != TokenLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
