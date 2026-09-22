package control

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/legion/fanctl/internal/config"
	"github.com/legion/fanctl/internal/hwmon"
	"github.com/legion/fanctl/internal/state"
)

const (
	defaultChip = "nct6683"

	// LowPercentWarn is the duty cycle below which restoring a fan unattended
	// is worth a line in the journal.
	LowPercentWarn = 20
)

// ErrNoProfile reports that nothing has been saved yet. Callers distinguish it
// from a failure so they can say "no saved profile" instead of "restore failed".
var ErrNoProfile = errors.New("no saved fan profile")

// FanInfo is the D-Bus-facing fan snapshot (signature fields of (issiiisb)).
type FanInfo struct {
	Index    int32
	Name     string
	Note     string
	RPM      int32
	Percent  int32
	Enable   int32 // -1 when pwmN_enable is absent
	Mode     string
	Writable bool
}

// FansHandler is called after a refresh or successful apply.
type FansHandler func(fans []FanInfo)

// Controller owns chip access, debounce, and header name resolution.
type Controller struct {
	mu       sync.Mutex
	chip     *hwmon.Chip
	cfg      *config.Config
	onChange FansHandler
	// userTouched records that something user-initiated has happened, which
	// closes the boot verify window so VerifySaved stops fighting the user.
	userTouched bool
}

// New creates a controller for the default nct6683 chip and headers config.
func New() (*Controller, error) {
	chip, err := hwmon.FindChip(defaultChip)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Find("")
	if err != nil {
		return nil, err
	}
	log.Printf("controller ready chip=%s path=%s headers=%v", chip.Name, chip.Path, cfg != nil && len(cfg.Headers) > 0)
	return &Controller{
		chip: chip,
		cfg:  cfg,
	}, nil
}

// SetChangeHandler registers a callback for fan list updates.
func (c *Controller) SetChangeHandler(fn FansHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onChange = fn
}

// GetFans returns the current fan list with header names.
func (c *Controller) GetFans() ([]FanInfo, error) {
	start := time.Now()
	c.mu.Lock()
	chip := c.chip
	cfg := c.cfg
	c.mu.Unlock()

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		log.Printf("GetFans error after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return nil, err
	}
	fans := fansFromStatus(status, cfg)
	log.Printf("GetFans ok count=%d in %s", len(fans), time.Since(start).Round(time.Millisecond))
	return fans, nil
}

// SetPercent writes one fan PWM percent immediately (GUI already debounces).
func (c *Controller) SetPercent(index uint32, percent byte) error {
	if percent > 100 {
		return fmt.Errorf("percent must be 0-100, got %d", percent)
	}
	idx := int(index)
	if idx <= 0 {
		return fmt.Errorf("invalid fan index %d", idx)
	}

	c.mu.Lock()
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.userTouched = true
	c.mu.Unlock()

	start := time.Now()
	if err := hwmon.SetPWM(chip, idx, int(percent)); err != nil {
		log.Printf("SetPercent apply fan=%d pct=%d failed after %s: %v", idx, percent, time.Since(start).Round(time.Millisecond), err)
		return err
	}
	log.Printf("SetPercent apply fan=%d pct=%d ok in %s", idx, percent, time.Since(start).Round(time.Millisecond))

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		log.Printf("SetPercent apply fan=%d: read status after write: %v", idx, err)
		return nil
	}
	if onChange != nil {
		onChange(fansFromStatus(status, cfg))
	}
	return nil
}

// SetMax sets all writable fans to 100%.
func (c *Controller) SetMax() error {
	start := time.Now()
	c.mu.Lock()
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.userTouched = true
	c.mu.Unlock()

	if err := hwmon.SetMax(chip); err != nil {
		log.Printf("SetMax failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return err
	}
	log.Printf("SetMax ok in %s", time.Since(start).Round(time.Millisecond))
	return c.emit(chip, cfg, onChange)
}

// SetAuto returns writable fans to firmware automatic control.
func (c *Controller) SetAuto() error {
	start := time.Now()
	c.mu.Lock()
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.userTouched = true
	c.mu.Unlock()

	if err := hwmon.SetAuto(chip); err != nil {
		log.Printf("SetAuto failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return err
	}
	log.Printf("SetAuto ok in %s", time.Since(start).Round(time.Millisecond))
	return c.emit(chip, cfg, onChange)
}

// SaveState snapshots the current hardware state as the profile to restore at
// the next boot.
func (c *Controller) SaveState() error {
	c.mu.Lock()
	chip := c.chip
	c.userTouched = true
	c.mu.Unlock()

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		return err
	}

	profile := state.FromStatus(chip.Name, status.Fans, time.Now())
	path := state.ResolvePath("")
	if err := state.Save(path, profile); err != nil {
		return err
	}

	for _, warn := range LowSpeedWarnings(profile, status.Fans) {
		log.Printf("SaveState: %s", warn)
	}
	log.Printf("SaveState ok fans=%d path=%s", len(profile.Fans), path)
	return nil
}

// RestoreState applies the saved profile and notifies subscribers.
func (c *Controller) RestoreState() (int, error) {
	applied, err := c.applySaved("RestoreState")
	if err != nil {
		return applied, err
	}
	c.EmitCurrent()
	return applied, nil
}

// VerifySaved re-applies saved fans the firmware has drifted away from. It is
// a no-op once anything user-initiated has happened, so it never fights the
// user; fanctld only calls it during a short window after boot.
func (c *Controller) VerifySaved() (int, error) {
	c.mu.Lock()
	touched := c.userTouched
	c.mu.Unlock()
	if touched {
		return 0, nil
	}
	return c.applySaved("VerifySaved")
}

// UserTouched reports whether anything user-initiated has been seen yet.
func (c *Controller) UserTouched() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.userTouched
}

// ApplyResult summarises one restore. Applied and Skipped are in fan index
// order; Errs are already wrapped with the fan they came from.
type ApplyResult struct {
	Applied []state.Action
	Skipped []state.Skip
	Errs    []error
}

// ApplyProfile writes a profile to the chip. One dead or read-only header must
// not sink the rest, so per-fan failures are collected rather than returned;
// the caller decides how to report them. Shared by fanctld and the CLI so the
// two cannot drift apart.
func ApplyProfile(chip *hwmon.Chip, profile *state.Profile, fans []hwmon.Fan) ApplyResult {
	actions, skipped := state.Plan(profile, fans)
	res := ApplyResult{Skipped: skipped}

	for _, a := range actions {
		var err error
		if a.Mode == state.ModeAuto {
			err = hwmon.SetAutoIndex(chip, a.Index)
		} else {
			err = hwmon.SetPWMRaw(chip, a.Index, a.PWM)
		}
		if err != nil {
			res.Errs = append(res.Errs, fmt.Errorf("fan%d: %w", a.Index, err))
			continue
		}
		res.Applied = append(res.Applied, a)
	}
	return res
}

// Err reports a failure only when every attempted write failed; a partial
// restore is still a restore.
func (r ApplyResult) Err() error {
	if len(r.Applied) == 0 && len(r.Errs) > 0 {
		return errors.Join(r.Errs...)
	}
	return nil
}

func (c *Controller) applySaved(who string) (int, error) {
	c.mu.Lock()
	chip := c.chip
	c.mu.Unlock()

	profile, err := state.LoadIfPresent(state.ResolvePath(""))
	if err != nil {
		return 0, err
	}
	if profile == nil {
		return 0, ErrNoProfile
	}
	if profile.Chip != "" && profile.Chip != chip.Name {
		// A partial apply beats a hard failure in the boot path, and Plan turns
		// indexes this board does not have into skips anyway.
		log.Printf("%s: profile was saved for chip %q, applying against %q", who, profile.Chip, chip.Name)
	}

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		return 0, err
	}

	res := ApplyProfile(chip, profile, status.Fans)
	for _, s := range res.Skipped {
		log.Printf("%s: fan%d skipped (%s)", who, s.Index, s.Reason)
	}
	for _, a := range res.Applied {
		if a.Mode == state.ModeAuto {
			log.Printf("%s: fan%d set to firmware auto", who, a.Index)
			continue
		}
		pct := hwmon.PWMToPercent(a.PWM)
		if pct < LowPercentWarn {
			log.Printf("%s: fan%d restored to pwm=%d (%d%%) - below %d%%, check temperatures",
				who, a.Index, a.PWM, pct, LowPercentWarn)
			continue
		}
		log.Printf("%s: fan%d restored to pwm=%d (%d%%)", who, a.Index, a.PWM, pct)
	}
	for _, e := range res.Errs {
		log.Printf("%s: %v", who, e)
	}

	return len(res.Applied), res.Err()
}

// LowSpeedWarnings flags saved fans that are spinning below LowPercentWarn, so
// a calibration that would starve a pump at every boot is visible when it is
// saved rather than after a reboot.
func LowSpeedWarnings(profile *state.Profile, fans []hwmon.Fan) []string {
	if profile == nil {
		return nil
	}
	rpm := make(map[int]int, len(fans))
	for _, f := range fans {
		rpm[f.Index] = f.RPM
	}

	var out []string
	for _, idx := range sortedFanIndexes(profile.Fans) {
		fs := profile.Fans[idx]
		if fs.Mode != state.ModeManual {
			continue
		}
		pct := hwmon.PWMToPercent(fs.PWM)
		if pct >= LowPercentWarn || rpm[idx] <= 0 {
			continue
		}
		out = append(out, fmt.Sprintf(
			"fan%d saved at %d%% (%d RPM) — below %d%%, it will be applied at every boot",
			idx, pct, rpm[idx], LowPercentWarn))
	}
	return out
}

func sortedFanIndexes(fans map[int]state.FanState) []int {
	out := make([]int, 0, len(fans))
	for idx := range fans {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

// ReloadConfig reloads headers.yaml from the default search path.
func (c *Controller) ReloadConfig() error {
	cfg, err := config.Find("")
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	log.Printf("ReloadConfig ok")
	return nil
}

// EmitCurrent reads hardware and notifies subscribers.
func (c *Controller) EmitCurrent() {
	c.mu.Lock()
	chip := c.chip
	cfg := c.cfg
	onChange := c.onChange
	c.mu.Unlock()
	_ = c.emit(chip, cfg, onChange)
}

func (c *Controller) emit(chip *hwmon.Chip, cfg *config.Config, onChange FansHandler) error {
	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		return err
	}
	if onChange != nil {
		onChange(fansFromStatus(status, cfg))
	}
	return nil
}

func fansFromStatus(status *hwmon.Status, cfg *config.Config) []FanInfo {
	out := make([]FanInfo, 0, len(status.Fans))
	for _, f := range status.Fans {
		info := FanInfo{
			Index:    int32(f.Index),
			RPM:      int32(f.RPM),
			Percent:  int32(f.Percent),
			Enable:   -1,
			Mode:     "-",
			Writable: f.Writable,
		}
		if f.Enable != nil {
			info.Enable = int32(*f.Enable)
			info.Mode = hwmon.ControlModeLabel(*f.Enable)
		}
		if h, ok := cfg.ByIndex(f.Index); ok {
			info.Name = h.Name
			info.Note = h.Note
		}
		if info.Name == "" {
			info.Name = fmt.Sprintf("fan%d", f.Index)
		}
		out = append(out, info)
	}
	return out
}
