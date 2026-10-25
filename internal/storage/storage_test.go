package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStorage_CRUD(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.json")

	s, err := NewStorage(cfgFile)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	// Check default config
	cfg := s.GetConfig()
	if cfg.Port != 8080 || cfg.AdminUsername != "admin" {
		t.Errorf("unexpected default config: %+v", cfg)
	}

	changeCh := make(chan []Rule, 1)
	s.OnChange(func(rules []Rule) {
		changeCh <- rules
	})

	// Add valid rule
	r1 := Rule{
		Name:        "Test API",
		Slug:        "test-api",
		TargetURL:   "http://example.com/api",
		StripPrefix: true,
		InjectBase:  true,
		Enabled:     true,
	}
	added, err := s.AddRule(r1)
	if err != nil {
		t.Fatalf("failed to add rule: %v", err)
	}
	if added.ID == "" || added.CreatedAt == 0 {
		t.Errorf("expected ID and CreatedAt to be set")
	}

	select {
	case notified := <-changeCh:
		if len(notified) != 1 {
			t.Errorf("expected 1 rule in notification, got %d", len(notified))
		}
	case <-time.After(time.Second):
		t.Errorf("timeout waiting for change notification")
	}

	// Add duplicate slug
	_, err = s.AddRule(Rule{Slug: "test-api", TargetURL: "http://other.com"})
	if err != ErrSlugExists {
		t.Errorf("expected ErrSlugExists, got %v", err)
	}

	// Add reserved slug
	_, err = s.AddRule(Rule{Slug: "admin", TargetURL: "http://other.com"})
	if err == nil {
		t.Errorf("expected error for reserved slug admin")
	}

	// Update rule
	added.Name = "Updated Name"
	updated, err := s.UpdateRule(added.ID, added)
	if err != nil {
		t.Fatalf("failed to update rule: %v", err)
	}
	if updated.Name != "Updated Name" {
		t.Errorf("expected updated name, got %s", updated.Name)
	}

	// Test persistence across restart
	s2, err := NewStorage(cfgFile)
	if err != nil {
		t.Fatalf("failed to reload storage: %v", err)
	}
	rules2 := s2.ListRules()
	if len(rules2) != 1 || rules2[0].Name != "Updated Name" {
		t.Errorf("unexpected reloaded rules: %+v", rules2)
	}

	// Delete rule
	if err := s2.DeleteRule(added.ID); err != nil {
		t.Fatalf("failed to delete rule: %v", err)
	}
	if len(s2.ListRules()) != 0 {
		t.Errorf("expected 0 rules after deletion")
	}
}
