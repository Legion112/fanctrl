package control

import (
	"testing"

	"github.com/legion/fanctl/internal/config"
	"github.com/legion/fanctl/internal/hwmon"
)

func TestFansFromStatus(t *testing.T) {
	cfg, err := config.Parse([]byte(`
chip: nct6683
headers:
  1: { name: CPU_FAN1, note: "radiator" }
`))
	if err != nil {
		t.Fatal(err)
	}
	enable := hwmon.EnableManual
	status := &hwmon.Status{
		Fans: []hwmon.Fan{
			{Index: 1, RPM: 1200, PWM: 128, Percent: 50, Enable: &enable, Writable: true},
			{Index: 2, RPM: 0, PWM: 0, Percent: 0, Writable: false},
		},
	}
	fans := fansFromStatus(status, cfg)
	if len(fans) != 2 {
		t.Fatalf("len=%d", len(fans))
	}
	if fans[0].Name != "CPU_FAN1" || fans[0].Note != "radiator" || fans[0].Mode != "manual" {
		t.Fatalf("fan0=%+v", fans[0])
	}
	if fans[1].Name != "fan2" || fans[1].Enable != -1 {
		t.Fatalf("fan1=%+v", fans[1])
	}
}
