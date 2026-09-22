// Package state persists the fan speeds a user settled on so fanctld can
// re-apply them at boot. It is machine-written, unlike internal/config, which
// is hand-edited; hence the atomic write and the strict schema check.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultDir is created by the Makefile and by fanctld's StateDirectory=.
	DefaultDir = "/var/lib/fanctl"
	// DefaultPath is the host-wide saved profile.
	DefaultPath = DefaultDir + "/state.yaml"
	// Version is the only schema this build understands.
	Version = 1
)

// statePath is the default profile path; overridden in tests.
var statePath = DefaultPath

// Mode is how a header should be driven. It is deliberately a word rather than
// the raw pwmN_enable integer: the ASRock DKMS driver inverts the hwmon
// convention (manual=1, auto=0), so a saved file must not encode that.
type Mode string

const (
	ModeManual Mode = "manual"
	ModeAuto   Mode = "auto"
)

// FanState is the saved setting for one header.
type FanState struct {
	Mode Mode `yaml:"mode"`
	// PWM is the authoritative duty cycle, 0-255. Percent round-trips are lossy
	// (pwm 100 -> 39% -> 99), and restore runs once per boot.
	PWM int `yaml:"pwm,omitempty"`
	// Percent is informational, for humans reading the file. Restore ignores it.
	Percent int `yaml:"percent,omitempty"`
}

// Profile is the whole saved snapshot.
type Profile struct {
	Version int              `yaml:"version"`
	Chip    string           `yaml:"chip"`
	SavedAt time.Time        `yaml:"saved_at"`
	Fans    map[int]FanState `yaml:"fans"`
	Path    string           `yaml:"-"` // file it was loaded from, if any
}

const fileHeader = `# fanctl saved fan profile.
# Written by "fanctl save" and the tray Save button; re-applied by fanctld at boot.
# Restore uses pwm (0-255); percent is informational only.
`

// ResolvePath picks the profile file: explicit, then $FANCTL_STATE, then
// /var/lib/fanctl/state.yaml. Unlike config.Find there is no search chain —
// this file is written, so it needs one unambiguous answer.
func ResolvePath(explicit string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("FANCTL_STATE")); s != "" {
		return s
	}
	return statePath
}

// Marshal renders a profile as commented YAML.
func Marshal(p *Profile) ([]byte, error) {
	if p == nil {
		return nil, errors.New("nil profile")
	}
	out := *p
	if out.Version == 0 {
		out.Version = Version
	}
	body, err := yaml.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("marshal profile: %w", err)
	}
	return append([]byte(fileHeader), body...), nil
}

// Parse unmarshals and validates a profile. An unrecognised version is a hard
// error: restore runs unattended, so guessing at an unknown schema is worse
// than doing nothing.
func Parse(data []byte) (*Profile, error) {
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if p.Version != Version {
		return nil, fmt.Errorf("unsupported state version %d (want %d)", p.Version, Version)
	}
	p.Chip = strings.TrimSpace(p.Chip)

	fans := make(map[int]FanState, len(p.Fans))
	for idx, fs := range p.Fans {
		if idx <= 0 {
			return nil, fmt.Errorf("invalid fan index %d (want positive integer)", idx)
		}
		switch fs.Mode {
		case ModeAuto:
			fs.PWM, fs.Percent = 0, 0
		case ModeManual:
			if fs.PWM < 0 || fs.PWM > 255 {
				return nil, fmt.Errorf("fan %d: pwm must be 0-255, got %d", idx, fs.PWM)
			}
		default:
			return nil, fmt.Errorf("fan %d: unknown mode %q (want %q or %q)",
				idx, fs.Mode, ModeManual, ModeAuto)
		}
		fans[idx] = fs
	}
	p.Fans = fans
	return &p, nil
}

// Load reads a profile from path.
func Load(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p.Path = path
	return p, nil
}

// LoadIfPresent returns (nil, nil) when no profile exists yet, mirroring
// config.Find: nothing saved is a normal state, not a failure.
func LoadIfPresent(path string) (*Profile, error) {
	p, err := Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return p, err
}

// Save writes the profile atomically: a torn file would be re-applied to the
// fans at the next boot.
func Save(path string, p *Profile) error {
	data, err := Marshal(p)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	// Same directory, so the rename below stays on one filesystem.
	tmp, err := os.CreateTemp(dir, ".state-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil { // CreateTemp makes 0600
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	renamed = true

	// This file's whole job is to survive a reboot, including an unclean one.
	// Without fsyncing the parent the rename itself can be lost.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
