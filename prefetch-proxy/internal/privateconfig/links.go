package privateconfig

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type linkEntry struct {
	body      []byte
	expiresAt time.Time
}

type LinkStore struct {
	mu      sync.Mutex
	entries map[string]linkEntry
	ttl     time.Duration
	now     func() time.Time
}

func NewLinkStore(ttl time.Duration, now func() time.Time) *LinkStore {
	return &LinkStore{entries: make(map[string]linkEntry), ttl: ttl, now: now}
}

func (s *LinkStore) Issue(nodes []Node) (string, error) {
	body, err := yaml.Marshal(struct {
		Proxies []Node `yaml:"proxies"`
	}{Proxies: nodes})
	if err != nil {
		return "", err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(random[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = linkEntry{body: body, expiresAt: s.now().Add(s.ttl)}
	time.AfterFunc(s.ttl, func() {
		s.mu.Lock()
		delete(s.entries, id)
		s.mu.Unlock()
	})
	return id, nil
}

func (s *LinkStore) Get(id string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	if !ok {
		return nil, false
	}
	if !s.now().Before(entry.expiresAt) {
		delete(s.entries, id)
		return nil, false
	}
	return entry.body, true
}
