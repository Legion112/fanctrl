package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultSystemPath(t *testing.T) {
	if DefaultSystemPath != "/etc/fanctl/headers.yaml" {
		t.Fatalf("DefaultSystemPath = %q", DefaultSystemPath)
	}
}

func TestCandidatePaths(t *testing.T) {
	t.Setenv("FANCTL_CONFIG", "")
	orig := systemConfigPath
	t.Cleanup(func() { systemConfigPath = orig })

	dir := t.TempDir()
	sys := filepath.Join(dir, "headers.yaml")
	systemConfigPath = sys

	got := candidatePaths("")
	want := []string{sys}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidatePaths() = %v, want %v", got, want)
	}

	env := filepath.Join(dir, "env.yaml")
	t.Setenv("FANCTL_CONFIG", env)
	got = candidatePaths("")
	want = []string{env, sys}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidatePaths with env = %v, want %v", got, want)
	}

	explicit := filepath.Join(dir, "explicit.yaml")
	got = candidatePaths(explicit)
	want = []string{explicit, env, sys}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidatePaths with explicit = %v, want %v", got, want)
	}
}

func TestFindUsesSystemPath(t *testing.T) {
	t.Setenv("FANCTL_CONFIG", "")
	orig := systemConfigPath
	t.Cleanup(func() { systemConfigPath = orig })

	dir := t.TempDir()
	sys := filepath.Join(dir, "headers.yaml")
	const yaml = `
chip: nct6683
headers:
  1: { name: CPU_FAN1, note: "AIO radiator FAN plug" }
`
	if err := os.WriteFile(sys, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	systemConfigPath = sys

	cfg, err := Find("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.Path != sys {
		t.Fatalf("Find() = %+v, want path %q", cfg, sys)
	}

	systemConfigPath = filepath.Join(dir, "missing.yaml")
	none, err := Find("")
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Fatalf("Find() = %+v, want nil", none)
	}
}
