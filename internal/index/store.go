package index

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

// snapshot is an immutable view used by readers.
type snapshot struct {
	servers  map[string]*ServerData
	byKey    map[string][]*Item
	items    int
	entries  []*Entry           // movies and series, newest first
	episodes map[string][]*Item // by server/showRatingKey, in season and episode order
}

// Store owns the indexed data. Writers replace a whole server at a time; readers never block.
type Store struct {
	mu      sync.Mutex // serializes writers
	servers map[string]*ServerData
	snap    atomic.Pointer[snapshot]
}

// NewStore returns an empty store.
func NewStore() *Store {
	s := &Store{servers: map[string]*ServerData{}}
	s.publish()
	return s
}

// PutServer replaces the data of one server. The ServerData and its items must not be modified afterwards.
func (s *Store) PutServer(sd *ServerData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers[sd.MachineID] = sd
	s.publish()
}

// RemoveServer drops a server that is no longer shared with the account.
func (s *Store) RemoveServer(machineID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[machineID]; ok {
		delete(s.servers, machineID)
		s.publish()
	}
}

// Server returns the current data of a server (read-only), or nil.
func (s *Store) Server(machineID string) *ServerData {
	return s.snap.Load().servers[machineID]
}

// Servers returns the machine identifiers present in the index.
func (s *Store) Servers() []string {
	snap := s.snap.Load()
	ids := make([]string, 0, len(snap.servers))
	for id := range snap.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Lookup returns the items for a key: "imdb:tt0816692" for movies, EpisodeKey(...) for episodes.
func (s *Store) Lookup(key string) []*Item {
	return s.snap.Load().byKey[key]
}

// Count returns the number of indexed items.
func (s *Store) Count() int { return s.snap.Load().items }

// publish rebuilds the lookup maps. Callers hold s.mu.
func (s *Store) publish() {
	next := &snapshot{servers: make(map[string]*ServerData, len(s.servers)), byKey: map[string][]*Item{}, episodes: map[string][]*Item{}}
	for id, sd := range s.servers {
		next.servers[id] = sd
		for _, it := range sd.Items {
			next.items++
			if it.Type == "episode" {
				ek := id + "/" + it.ShowKey
				next.episodes[ek] = append(next.episodes[ek], it)
				show := sd.Shows[it.ShowKey]
				if show == nil {
					continue
				}
				for _, k := range show.IDs.Keys() {
					ek := EpisodeKey(k, it.Season, it.Episode)
					next.byKey[ek] = append(next.byKey[ek], it)
				}
				continue
			}
			for _, k := range it.IDs.Keys() {
				next.byKey[k] = append(next.byKey[k], it)
			}
		}
	}
	buildEntries(next)
	s.snap.Store(next)
}

const snapshotVersion = 2 // 2: artwork, sections and external subtitles

type fileFormat struct {
	Version int
	Servers []*ServerData
}

// Save writes the index to path atomically.
func (s *Store) Save(path string) error {
	snap := s.snap.Load()
	ff := fileFormat{Version: snapshotVersion}
	for _, sd := range snap.servers {
		ff.Servers = append(ff.Servers, sd)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := gob.NewEncoder(tmp).Encode(ff); err != nil {
		tmp.Close()
		return fmt.Errorf("index: encoding snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a snapshot written by Save. A missing file is not an error.
func (s *Store) Load(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var ff fileFormat
	if err := gob.NewDecoder(f).Decode(&ff); err != nil {
		return fmt.Errorf("index: decoding snapshot: %w", err)
	}
	if ff.Version != snapshotVersion {
		return nil // incompatible snapshot: start empty and resync
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sd := range ff.Servers {
		if sd.Items == nil {
			sd.Items = map[string]*Item{}
		}
		if sd.Shows == nil {
			sd.Shows = map[string]*Show{}
		}
		s.servers[sd.MachineID] = sd
	}
	s.publish()
	return nil
}
