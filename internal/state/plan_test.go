package state

import (
	"testing"
	"time"

	"github.com/legion/fanctl/internal/hwmon"
)

func enable(v int) *int { return &v }

func manualFan(idx, pwm int) hwmon.Fan {
	return hwmon.Fan{
		Index: idx, PWM: pwm, Percent: hwmon.PWMToPercent(pwm),
		Enable: enable(hwmon.EnableManual), Writable: true,
	}
}

func autoFan(idx, pwm int) hwmon.Fan {
	return hwmon.Fan{
		Index: idx, PWM: pwm, Percent: hwmon.PWMToPercent(pwm),
		Enable: enable(hwmon.EnableAuto), Writable: true,
	}
}

func profile(fans map[int]FanState) *Profile {
	return &Profile{Version: Version, Chip: "nct6683", Fans: fans}
}

func TestPlan(t *testing.T) {
	tests := []struct {
		name        string
		profile     *Profile
		fans        []hwmon.Fan
		wantActions []Action
		wantSkips   []Skip
	}{
		{
			name:      "manual already at saved pwm",
			profile:   profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}}),
			fans:      []hwmon.Fan{manualFan(1, 128)},
			wantSkips: []Skip{{1, SkipAlreadyApplied}},
		},
		{
			name:        "manual at a different pwm",
			profile:     profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}}),
			fans:        []hwmon.Fan{manualFan(1, 200)},
			wantActions: []Action{{Index: 1, Mode: ModeManual, PWM: 128}},
		},
		{
			name:        "right pwm but firmware is driving it",
			profile:     profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}}),
			fans:        []hwmon.Fan{autoFan(1, 128)},
			wantActions: []Action{{Index: 1, Mode: ModeManual, PWM: 128}},
		},
		{
			name:        "saved auto but currently manual",
			profile:     profile(map[int]FanState{2: {Mode: ModeAuto}}),
			fans:        []hwmon.Fan{manualFan(2, 90)},
			wantActions: []Action{{Index: 2, Mode: ModeAuto}},
		},
		{
			name:      "saved auto and already auto",
			profile:   profile(map[int]FanState{2: {Mode: ModeAuto}}),
			fans:      []hwmon.Fan{autoFan(2, 90)},
			wantSkips: []Skip{{2, SkipAlreadyApplied}},
		},
		{
			name:    "missing header does not stop the rest",
			profile: profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}, 9: {Mode: ModeManual, PWM: 200}}),
			fans:    []hwmon.Fan{manualFan(1, 10)},
			wantActions: []Action{
				{Index: 1, Mode: ModeManual, PWM: 128},
			},
			wantSkips: []Skip{{9, SkipNotPresent}},
		},
		{
			name:      "read-only header",
			profile:   profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}}),
			fans:      []hwmon.Fan{{Index: 1, PWM: 10, Writable: false}},
			wantSkips: []Skip{{1, SkipReadOnly}},
		},
		{
			name:        "no enable file counts as manual",
			profile:     profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}}),
			fans:        []hwmon.Fan{{Index: 1, PWM: 10, Writable: true}},
			wantActions: []Action{{Index: 1, Mode: ModeManual, PWM: 128}},
		},
		{
			name:    "hardware fan absent from the profile is untouched",
			profile: profile(map[int]FanState{1: {Mode: ModeManual, PWM: 128}}),
			fans:    []hwmon.Fan{manualFan(1, 128), manualFan(5, 60)},
			wantSkips: []Skip{
				{1, SkipAlreadyApplied},
			},
		},
		{
			name:    "empty profile",
			profile: profile(map[int]FanState{}),
			fans:    []hwmon.Fan{manualFan(1, 128)},
		},
		{
			name: "nil profile",
			fans: []hwmon.Fan{manualFan(1, 128)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actions, skipped := Plan(tc.profile, tc.fans)
			if len(actions) != len(tc.wantActions) {
				t.Fatalf("actions = %+v, want %+v", actions, tc.wantActions)
			}
			for i, want := range tc.wantActions {
				if actions[i] != want {
					t.Fatalf("action %d = %+v, want %+v", i, actions[i], want)
				}
			}
			if len(skipped) != len(tc.wantSkips) {
				t.Fatalf("skipped = %+v, want %+v", skipped, tc.wantSkips)
			}
			for i, want := range tc.wantSkips {
				if skipped[i] != want {
					t.Fatalf("skip %d = %+v, want %+v", i, skipped[i], want)
				}
			}
		})
	}
}

func TestPlanOrdersByIndex(t *testing.T) {
	p := profile(map[int]FanState{
		6: {Mode: ModeManual, PWM: 10},
		1: {Mode: ModeManual, PWM: 20},
		3: {Mode: ModeManual, PWM: 30},
	})
	fans := []hwmon.Fan{manualFan(6, 0), manualFan(1, 0), manualFan(3, 0)}
	for i := 0; i < 20; i++ {
		actions, _ := Plan(p, fans)
		if len(actions) != 3 {
			t.Fatalf("actions = %+v", actions)
		}
		if actions[0].Index != 1 || actions[1].Index != 3 || actions[2].Index != 6 {
			t.Fatalf("actions out of order: %+v", actions)
		}
	}
}

func TestFromStatus(t *testing.T) {
	now := time.Date(2026, 9, 22, 14, 3, 11, 500, time.UTC)
	fans := []hwmon.Fan{
		manualFan(1, 128),
		autoFan(2, 90),
		{Index: 3, PWM: 200, Percent: 78, Writable: false},                  // read-only
		{Index: 4, PWM: 64, Percent: 25, Writable: true},                    // no enable file
		{Index: 5, PWM: 32, Percent: 13, Enable: enable(7), Writable: true}, // undocumented mode
	}

	p := FromStatus("nct6683", fans, now)

	if p.Version != Version || p.Chip != "nct6683" {
		t.Fatalf("header = %d/%q", p.Version, p.Chip)
	}
	if !p.SavedAt.Equal(now.Truncate(time.Second)) {
		t.Fatalf("SavedAt = %v", p.SavedAt)
	}
	if _, ok := p.Fans[3]; ok {
		t.Fatalf("read-only fan was recorded: %+v", p.Fans)
	}
	if got := p.Fans[1]; got != (FanState{Mode: ModeManual, PWM: 128, Percent: 50}) {
		t.Fatalf("fan1 = %+v", got)
	}
	if got := p.Fans[2]; got != (FanState{Mode: ModeAuto}) {
		t.Fatalf("fan2 = %+v, want auto with no duty cycle", got)
	}
	if got := p.Fans[4]; got.Mode != ModeManual || got.PWM != 64 {
		t.Fatalf("fan4 = %+v, want manual", got)
	}
	if got := p.Fans[5]; got.Mode != ModeManual || got.PWM != 32 {
		t.Fatalf("fan5 = %+v, want manual", got)
	}
}

// The strongest single assertion here: save the live state, round-trip it
// through YAML, and planning it back against the same hardware must be a no-op.
// Catches pwm/percent confusion, map key type bugs, and mode mapping bugs at once.
func TestSaveThenRestoreIsANoOp(t *testing.T) {
	fans := []hwmon.Fan{
		manualFan(1, 128),
		autoFan(2, 90),
		manualFan(3, 100), // 100 -> 39% -> 99 if percent were authoritative
		{Index: 4, PWM: 255, Percent: 100, Writable: true},
		{Index: 5, PWM: 200, Writable: false},
	}

	data, err := Marshal(FromStatus("nct6683", fans, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", data, err)
	}

	actions, _ := Plan(p, fans)
	if len(actions) != 0 {
		t.Fatalf("restoring a just-saved profile would write %+v", actions)
	}
}
