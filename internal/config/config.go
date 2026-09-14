package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultSystemPath is the host-wide headers config location.
const DefaultSystemPath = "/etc/fanctl/headers.yaml"

// systemConfigPath is the default system config path; overridden in tests.
var systemConfigPath = DefaultSystemPath

// Header describes one motherboard fan header mapped to a hwmon index.
type Header struct {
	Name string `yaml:"name"`
	Note string `yaml:"note"`
}

// Config maps hwmon fan/pwm indexes to silk-screen names.
type Config struct {
	Chip    string         `yaml:"chip"`
	Headers map[int]Header `yaml:"headers"`
	Path    string         `yaml:"-"` // file that was loaded, if any
}

// Load reads a headers config from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

// Parse unmarshals YAML headers config.
func Parse(data []byte) (*Config, error) {
	var raw struct {
		Chip    string            `yaml:"chip"`
		Headers map[string]Header `yaml:"headers"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	cfg := &Config{
		Chip:    strings.TrimSpace(raw.Chip),
		Headers: make(map[int]Header, len(raw.Headers)),
	}
	for key, h := range raw.Headers {
		idx, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || idx <= 0 {
			return nil, fmt.Errorf("invalid header index %q (want positive integer)", key)
		}
		h.Name = strings.TrimSpace(h.Name)
		h.Note = strings.TrimSpace(h.Note)
		cfg.Headers[idx] = h
	}
	return cfg, nil
}

// Find searches for a config file. Order: explicit path, FANCTL_CONFIG,
// then /etc/fanctl/headers.yaml.
// Returns nil, nil when no file is found (missing config is OK).
// If explicit is set and the file is missing, that is an error.
func Find(explicit string) (*Config, error) {
	if explicit != "" {
		return Load(explicit)
	}
	paths := candidatePaths("")
	for _, path := range paths {
		if path == "" {
			continue
		}
		cfg, err := Load(path)
		if err == nil {
			return cfg, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, nil
}

func candidatePaths(explicit string) []string {
	var paths []string
	if explicit != "" {
		paths = append(paths, explicit)
	}
	if env := strings.TrimSpace(os.Getenv("FANCTL_CONFIG")); env != "" {
		paths = append(paths, env)
	}
	paths = append(paths, systemConfigPath)
	return paths
}

// ByIndex returns the header for a hwmon index, or false if unset.
func (c *Config) ByIndex(index int) (Header, bool) {
	if c == nil {
		return Header{}, false
	}
	h, ok := c.Headers[index]
	return h, ok
}

// IndexByName resolves a silk-screen name to a hwmon index.
// Matching is case-insensitive; trailing "/WP" (water pump) is ignored.
func (c *Config) IndexByName(name string) (int, error) {
	if c == nil || len(c.Headers) == 0 {
		return 0, fmt.Errorf("no header config loaded; run sudo fanctl init-config or pass -config")
	}
	want := normalizeName(name)
	if want == "" {
		return 0, fmt.Errorf("empty header name")
	}
	var matches []int
	for idx, h := range c.Headers {
		if normalizeName(h.Name) == want {
			matches = append(matches, idx)
		}
	}
	if len(matches) == 0 {
		return 0, fmt.Errorf("header %q not found in config", name)
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("header %q matches multiple indexes %v", name, matches)
	}
	return matches[0], nil
}

func normalizeName(name string) string {
	s := strings.ToUpper(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.TrimSuffix(s, "/WP")
	return s
}
