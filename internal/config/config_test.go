package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/legion/fanctl/internal/config"
)

const sampleYAML = `
chip: nct6683
headers:
  1: { name: CPU_FAN1, note: "AIO radiator FAN plug" }
  2: { name: CPU_FAN2/WP, note: "AIO PUMP 4-pin" }
  6: { name: SB_FAN1, note: "chipset" }
`

func TestParse(t *testing.T) {
	cfg, err := config.Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Chip != "nct6683" {
		t.Fatalf("chip = %q", cfg.Chip)
	}
	h, ok := cfg.ByIndex(1)
	if !ok || h.Name != "CPU_FAN1" || h.Note != "AIO radiator FAN plug" {
		t.Fatalf("header 1 = %+v ok=%v", h, ok)
	}
	if _, ok := cfg.ByIndex(99); ok {
		t.Fatal("expected missing index")
	}
}

func TestParseInvalidIndex(t *testing.T) {
	_, err := config.Parse([]byte("headers:\n  zero: { name: X }\n"))
	if err == nil {
		t.Fatal("expected error for non-integer key")
	}
}

func TestIndexByName(t *testing.T) {
	cfg, err := config.Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		want int
	}{
		{"CPU_FAN1", 1},
		{"cpu_fan1", 1},
		{"CPU_FAN2/WP", 2},
		{"CPU_FAN2", 2},
		{"sb_fan1", 6},
	}
	for _, tc := range tests {
		got, err := cfg.IndexByName(tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}

	if _, err := cfg.IndexByName("NOPE"); err == nil {
		t.Fatal("expected missing name error")
	}
	if _, err := (*config.Config)(nil).IndexByName("CPU_FAN1"); err == nil {
		t.Fatal("expected nil config error")
	}
}

func TestIndexByNameDuplicate(t *testing.T) {
	cfg, err := config.Parse([]byte(`
headers:
  1: { name: SAME }
  2: { name: same }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.IndexByName("SAME"); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestLoadAndFind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "headers.yaml")
	if err := os.WriteFile(path, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != path {
		t.Fatalf("path = %q", cfg.Path)
	}

	found, err := config.Find(path)
	if err != nil || found == nil {
		t.Fatalf("Find explicit: %v cfg=%v", err, found)
	}

	if _, err := config.Find(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Fatal("expected error for missing -config path")
	}

	t.Setenv("FANCTL_CONFIG", "")
	// May be non-nil if /etc/fanctl/headers.yaml exists on this machine.
	if _, err := config.Find(""); err != nil {
		t.Fatal(err)
	}
}

func TestExampleEmbed(t *testing.T) {
	cfg, err := config.Parse(config.ExampleX570CreatorYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Chip != "nct6683" {
		t.Fatalf("chip = %q", cfg.Chip)
	}
	h, ok := cfg.ByIndex(6)
	if !ok || h.Name != "SB_FAN1" {
		t.Fatalf("header 6 = %+v", h)
	}
}
