package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleProfile() *Profile {
	return &Profile{
		Version: Version,
		Chip:    "nct6683",
		SavedAt: time.Date(2026, 9, 22, 14, 3, 11, 0, time.UTC),
		Fans: map[int]FanState{
			1: {Mode: ModeManual, PWM: 128, Percent: 50},
			2: {Mode: ModeAuto},
			4: {Mode: ModeManual, PWM: 255, Percent: 100},
		},
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	want := sampleProfile()
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", data, err)
	}

	if got.Version != want.Version || got.Chip != want.Chip {
		t.Fatalf("header = %d/%q, want %d/%q", got.Version, got.Chip, want.Version, want.Chip)
	}
	if !got.SavedAt.Equal(want.SavedAt) {
		t.Fatalf("SavedAt = %v, want %v", got.SavedAt, want.SavedAt)
	}
	if len(got.Fans) != len(want.Fans) {
		t.Fatalf("fans = %v, want %v", got.Fans, want.Fans)
	}
	for idx, w := range want.Fans {
		if got.Fans[idx] != w {
			t.Fatalf("fan %d = %+v, want %+v", idx, got.Fans[idx], w)
		}
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"future version", "version: 2\nchip: nct6683\nfans: {}\n"},
		{"missing version", "chip: nct6683\nfans: {}\n"},
		{"zero index", "version: 1\nfans:\n  0: { mode: manual, pwm: 10 }\n"},
		{"negative index", "version: 1\nfans:\n  -1: { mode: manual, pwm: 10 }\n"},
		{"unknown mode", "version: 1\nfans:\n  1: { mode: turbo, pwm: 10 }\n"},
		{"empty mode", "version: 1\nfans:\n  1: { pwm: 10 }\n"},
		{"pwm out of range", "version: 1\nfans:\n  1: { mode: manual, pwm: 999 }\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", tc.yaml)
			}
		})
	}
}

func TestParseDropsPWMOnAutoFans(t *testing.T) {
	p, err := Parse([]byte("version: 1\nfans:\n  1: { mode: auto, pwm: 200, percent: 78 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Fans[1]; got.PWM != 0 || got.Percent != 0 {
		t.Fatalf("auto fan kept duty cycle: %+v", got)
	}
}

func TestSaveCreatesDirAndSetsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.yaml")
	if err := Save(path, sampleProfile()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("file mode = %o, want 644", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o755 {
		t.Fatalf("dir mode = %o, want 755", got)
	}
}

func TestSaveReplacesLargerFileAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := os.WriteFile(path, []byte(strings.Repeat("# junk\n", 500)), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Save(path, sampleProfile()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "junk") {
		t.Fatalf("old content survived the write:\n%s", data)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("rewritten file does not parse: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLoadIfPresentMissingFile(t *testing.T) {
	p, err := LoadIfPresent(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if p != nil {
		t.Fatalf("profile = %+v, want nil", p)
	}
}

func TestLoadSetsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	if err := Save(path, sampleProfile()); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != path {
		t.Fatalf("Path = %q, want %q", p.Path, path)
	}
}

func TestResolvePathOrder(t *testing.T) {
	t.Setenv("FANCTL_STATE", "")
	orig := statePath
	t.Cleanup(func() { statePath = orig })

	dir := t.TempDir()
	sys := filepath.Join(dir, "state.yaml")
	statePath = sys

	if got := ResolvePath(""); got != sys {
		t.Fatalf("ResolvePath(\"\") = %q, want %q", got, sys)
	}

	env := filepath.Join(dir, "env.yaml")
	t.Setenv("FANCTL_STATE", env)
	if got := ResolvePath(""); got != env {
		t.Fatalf("ResolvePath(\"\") with env = %q, want %q", got, env)
	}

	explicit := filepath.Join(dir, "explicit.yaml")
	if got := ResolvePath(explicit); got != explicit {
		t.Fatalf("ResolvePath(explicit) = %q, want %q", got, explicit)
	}
}

func TestDefaultPath(t *testing.T) {
	if DefaultPath != "/var/lib/fanctl/state.yaml" {
		t.Fatalf("DefaultPath = %q", DefaultPath)
	}
}
