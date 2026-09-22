package state

import (
	"sort"
	"time"

	"github.com/legion/fanctl/internal/hwmon"
)

// Reasons a profile entry was not applied.
const (
	SkipNotPresent     = "not present"
	SkipReadOnly       = "read-only"
	SkipAlreadyApplied = "already applied"
)

// Action is one write restore must perform.
type Action struct {
	Index int
	Mode  Mode
	PWM   int // only meaningful when Mode is ModeManual
}

// Skip records a profile entry restore deliberately left alone.
type Skip struct {
	Index  int
	Reason string
}

// FromStatus snapshots live hardware into a savable profile. Read-only headers
// are omitted: nothing about them could be restored, so recording them would
// only produce skip noise at every boot.
func FromStatus(chipName string, fans []hwmon.Fan, now time.Time) *Profile {
	p := &Profile{
		Version: Version,
		Chip:    chipName,
		SavedAt: now.UTC().Truncate(time.Second),
		Fans:    make(map[int]FanState, len(fans)),
	}
	for _, f := range fans {
		if !f.Writable || f.Index <= 0 {
			continue
		}
		if f.Enable != nil && *f.Enable == hwmon.EnableAuto {
			p.Fans[f.Index] = FanState{Mode: ModeAuto}
			continue
		}
		// Manual, no enable file at all, or a mode this driver does not
		// document: recording the duty cycle is the safe reading.
		p.Fans[f.Index] = FanState{Mode: ModeManual, PWM: f.PWM, Percent: f.Percent}
	}
	return p
}

// Plan computes the minimum set of writes that brings fans to p. Headers
// present in hardware but absent from the profile are left untouched, and a
// header in the profile that is missing or read-only is skipped rather than
// failing the whole restore.
func Plan(p *Profile, fans []hwmon.Fan) (actions []Action, skipped []Skip) {
	if p == nil || len(p.Fans) == 0 {
		return nil, nil
	}

	live := make(map[int]hwmon.Fan, len(fans))
	for _, f := range fans {
		live[f.Index] = f
	}

	indexes := make([]int, 0, len(p.Fans))
	for idx := range p.Fans {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	for _, idx := range indexes {
		want := p.Fans[idx]
		f, ok := live[idx]
		if !ok {
			skipped = append(skipped, Skip{Index: idx, Reason: SkipNotPresent})
			continue
		}
		if !f.Writable {
			skipped = append(skipped, Skip{Index: idx, Reason: SkipReadOnly})
			continue
		}

		if want.Mode == ModeAuto {
			if f.Enable != nil && *f.Enable == hwmon.EnableAuto {
				skipped = append(skipped, Skip{Index: idx, Reason: SkipAlreadyApplied})
				continue
			}
			actions = append(actions, Action{Index: idx, Mode: ModeAuto})
			continue
		}

		inManual := f.Enable == nil || *f.Enable == hwmon.EnableManual
		if inManual && f.PWM == want.PWM {
			skipped = append(skipped, Skip{Index: idx, Reason: SkipAlreadyApplied})
			continue
		}
		actions = append(actions, Action{Index: idx, Mode: ModeManual, PWM: want.PWM})
	}
	return actions, skipped
}
