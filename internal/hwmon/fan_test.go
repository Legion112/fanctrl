package hwmon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/legion/fanctl/internal/hwmon"
)

func TestPercentToPWM(t *testing.T) {
	tests := []struct {
		percent int
		want    int
	}{
		{0, 0},
		{50, 128},
		{100, 255},
		{41, 105},
	}
	for _, tc := range tests {
		got, err := hwmon.PercentToPWM(tc.percent)
		if err != nil {
			t.Fatalf("percent %d: %v", tc.percent, err)
		}
		if got != tc.want {
			t.Fatalf("PercentToPWM(%d) = %d, want %d", tc.percent, got, tc.want)
		}
	}
}

func TestPercentToPWMInvalid(t *testing.T) {
	if _, err := hwmon.PercentToPWM(-1); err == nil {
		t.Fatal("expected error for negative percent")
	}
	if _, err := hwmon.PercentToPWM(101); err == nil {
		t.Fatal("expected error for percent > 100")
	}
}

func TestPWMToPercent(t *testing.T) {
	tests := []struct {
		pwm  int
		want int
	}{
		{0, 0},
		{255, 100},
		{128, 50},
		{197, 77},
	}
	for _, tc := range tests {
		got := hwmon.PWMToPercent(tc.pwm)
		if got != tc.want {
			t.Fatalf("PWMToPercent(%d) = %d, want %d", tc.pwm, got, tc.want)
		}
	}
}

func TestFindChipIn(t *testing.T) {
	root := t.TempDir()
	chipDir := filepath.Join(root, "hwmon5")
	if err := os.Mkdir(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chipDir, "name"), []byte("nct6683\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	chip, err := hwmon.FindChipIn(root, "nct6683")
	if err != nil {
		t.Fatal(err)
	}
	if chip.Path != chipDir {
		t.Fatalf("chip path = %q, want %q", chip.Path, chipDir)
	}
}

func TestReadStatus(t *testing.T) {
	root := t.TempDir()
	chipDir := filepath.Join(root, "hwmon1")
	if err := os.Mkdir(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(chipDir, "name"), "nct6683\n")
	writeFile(t, filepath.Join(chipDir, "fan1_input"), "2500\n")
	writeFile(t, filepath.Join(chipDir, "pwm1"), "255\n")
	writeFile(t, filepath.Join(chipDir, "pwm1_enable"), "2\n")
	writeFile(t, filepath.Join(chipDir, "temp1_input"), "84000\n")
	writeFile(t, filepath.Join(chipDir, "temp1_label"), "CPU\n")

	chip := &hwmon.Chip{Path: chipDir, Name: "nct6683"}
	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Fans) != 1 {
		t.Fatalf("fans = %d, want 1", len(status.Fans))
	}
	if status.Fans[0].RPM != 2500 || status.Fans[0].Percent != 100 {
		t.Fatalf("fan1 = %+v", status.Fans[0])
	}
	if len(status.Temperatures) != 1 || status.Temperatures[0].C != 84 {
		t.Fatalf("temps = %+v", status.Temperatures)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
