package storage

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrRuleNotFound  = errors.New("rule not found")
	ErrSlugExists    = errors.New("slug already exists")
	ErrInvalidSlug   = errors.New("invalid slug: only alphanumeric, '-' and '_' are allowed, length 1-64")
	ErrInvalidTarget = errors.New("invalid target_url: must be a valid http or https URL")
	slugRegexp       = regexp.MustCompile("^[a-zA-Z0-9_-]{1,64}$")
)

// Rule represents a reverse proxy routing rule
type Rule struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	TargetURL   string            `json:"target_url"`
	StripPrefix bool              `json:"strip_prefix"`
	InjectBase  bool              `json:"inject_base"`
	PassHost    bool              `json:"pass_host"`
	Headers     map[string]string `json:"headers,omitempty"`
	Enabled     bool              `json:"enabled"`
	CreatedAt   int64             `json:"created_at"`
	UpdatedAt   int64             `json:"updated_at"`
}

// Config represents the application configuration persisted in JSON
type Config struct {
	Port          int    `json:"port"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
	AuthSecret    string `json:"auth_secret"`
	Rules         []Rule `json:"rules"`
}

// Validate validates rule fields
func (r *Rule) Validate() error {
	r.Slug = strings.TrimSpace(r.Slug)
	if !slugRegexp.MatchString(r.Slug) {
		return ErrInvalidSlug
	}
	lowerSlug := strings.ToLower(r.Slug)
	if lowerSlug == "admin" || lowerSlug == "api" || lowerSlug == "p" || lowerSlug == "favicon.ico" {
		return errors.New("slug cannot be a reserved keyword ('admin', 'api', 'p')")
	}

	r.TargetURL = strings.TrimSpace(r.TargetURL)
	u, err := url.ParseRequestURI(r.TargetURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ErrInvalidTarget
	}

	if r.Name == "" {
		r.Name = r.Slug
	}
	return nil
}

// Clone creates a deep copy of a rule
func (r *Rule) Clone() Rule {
	c := *r
	if r.Headers != nil {
		c.Headers = make(map[string]string, len(r.Headers))
		for k, v := range r.Headers {
			c.Headers[k] = v
		}
	}
	return c
}
