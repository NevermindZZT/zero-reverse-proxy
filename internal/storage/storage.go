package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Storage handles thread-safe configuration and rule persistence
type Storage struct {
	mu       sync.RWMutex
	filePath string
	config   Config
	onChange []func(rules []Rule)
}

// NewStorage creates or loads storage from the given JSON file path
func NewStorage(filePath string) (*Storage, error) {
	s := &Storage{
		filePath: filePath,
	}

	if err := s.loadOrCreate(); err != nil {
		return nil, err
	}
	return s, nil
}

// OnChange registers a callback when rules are updated
func (s *Storage) OnChange(fn func(rules []Rule)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

func (s *Storage) notifyChangeLocked() {
	rulesCopy := make([]Rule, len(s.config.Rules))
	for i, r := range s.config.Rules {
		rulesCopy[i] = r.Clone()
	}
	for _, fn := range s.onChange {
		go fn(rulesCopy)
	}
}

func (s *Storage) loadOrCreate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dir := filepath.Dir(s.filePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create data directory: %w", err)
		}
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create default configuration
			s.config = Config{
				Port:          8080,
				AdminUsername: "admin",
				AdminPassword: "admin123",
				AuthSecret:    randomHex(32),
				Rules:         []Rule{},
			}
			return s.saveLocked()
		}
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, &s.config); err != nil {
		return fmt.Errorf("invalid json in config file: %w", err)
	}

	if s.config.Port <= 0 {
		s.config.Port = 8080
	}
	if s.config.AdminUsername == "" {
		s.config.AdminUsername = "admin"
	}
	if s.config.AdminPassword == "" {
		s.config.AdminPassword = "admin123"
	}
	if s.config.AuthSecret == "" {
		s.config.AuthSecret = randomHex(32)
		_ = s.saveLocked()
	}
	if s.config.Rules == nil {
		s.config.Rules = []Rule{}
	}

	return nil
}

func (s *Storage) saveLocked() error {
	data, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write tmp config: %w", err)
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		return fmt.Errorf("failed to replace config file: %w", err)
	}

	return nil
}

// GetConfig returns a copy of the current configuration
func (s *Storage) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	rules := make([]Rule, len(s.config.Rules))
	for i, r := range s.config.Rules {
		rules[i] = r.Clone()
	}
	c.Rules = rules
	return c
}

// UpdateAdminPassword changes the admin password
func (s *Storage) UpdateAdminPassword(newPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.AdminPassword = newPassword
	return s.saveLocked()
}

// SetAdminCredentials updates admin credentials
func (s *Storage) SetAdminCredentials(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if username != "" {
		s.config.AdminUsername = username
	}
	if password != "" {
		s.config.AdminPassword = password
	}
	return s.saveLocked()
}

// ListRules returns all rules
func (s *Storage) ListRules() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rules := make([]Rule, len(s.config.Rules))
	for i, r := range s.config.Rules {
		rules[i] = r.Clone()
	}
	return rules
}

// GetRule retrieves a rule by ID
func (s *Storage) GetRule(id string) (Rule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.config.Rules {
		if r.ID == id {
			return r.Clone(), true
		}
	}
	return Rule{}, false
}

// GetRuleBySlug retrieves a rule by its path slug
func (s *Storage) GetRuleBySlug(slug string) (Rule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.config.Rules {
		if strings.EqualFold(r.Slug, slug) {
			return r.Clone(), true
		}
	}
	return Rule{}, false
}

// AddRule adds a new rule
func (s *Storage) AddRule(rule Rule) (Rule, error) {
	if err := rule.Validate(); err != nil {
		return Rule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range s.config.Rules {
		if strings.EqualFold(r.Slug, rule.Slug) {
			return Rule{}, ErrSlugExists
		}
	}

	if rule.ID == "" {
		rule.ID = randomHex(8)
	}
	now := time.Now().Unix()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	s.config.Rules = append(s.config.Rules, rule)
	if err := s.saveLocked(); err != nil {
		return Rule{}, err
	}

	s.notifyChangeLocked()
	return rule.Clone(), nil
}

// UpdateRule updates an existing rule by ID
func (s *Storage) UpdateRule(id string, updated Rule) (Rule, error) {
	if err := updated.Validate(); err != nil {
		return Rule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, r := range s.config.Rules {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Rule{}, ErrRuleNotFound
	}

	// Check slug conflict with other rules
	for i, r := range s.config.Rules {
		if i != idx && strings.EqualFold(r.Slug, updated.Slug) {
			return Rule{}, ErrSlugExists
		}
	}

	updated.ID = id
	updated.CreatedAt = s.config.Rules[idx].CreatedAt
	updated.UpdatedAt = time.Now().Unix()

	s.config.Rules[idx] = updated
	if err := s.saveLocked(); err != nil {
		return Rule{}, err
	}

	s.notifyChangeLocked()
	return updated.Clone(), nil
}

// DeleteRule deletes a rule by ID
func (s *Storage) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, r := range s.config.Rules {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrRuleNotFound
	}

	s.config.Rules = append(s.config.Rules[:idx], s.config.Rules[idx+1:]...)
	if err := s.saveLocked(); err != nil {
		return err
	}

	s.notifyChangeLocked()
	return nil
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
