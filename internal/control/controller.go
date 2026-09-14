package control

import (
	"fmt"
	"sync"
	"time"

	"github.com/legion/fanctl/internal/config"
	"github.com/legion/fanctl/internal/hwmon"
)

const (
	debounceDelay = 200 * time.Millisecond
	defaultChip   = "nct6683"
)

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
	pending  map[int]int // index -> percent
	timers   map[int]*time.Timer
	onChange FansHandler
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
	return &Controller{
		chip:    chip,
		cfg:     cfg,
		pending: make(map[int]int),
		timers:  make(map[int]*time.Timer),
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
	c.mu.Lock()
	chip := c.chip
	cfg := c.cfg
	c.mu.Unlock()

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		return nil, err
	}
	return fansFromStatus(status, cfg), nil
}

// SetPercent schedules a debounced PWM write for one fan index.
func (c *Controller) SetPercent(index uint32, percent byte) error {
	if percent > 100 {
		return fmt.Errorf("percent must be 0-100, got %d", percent)
	}
	idx := int(index)
	if idx <= 0 {
		return fmt.Errorf("invalid fan index %d", idx)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.pending[idx] = int(percent)
	if t, ok := c.timers[idx]; ok {
		t.Stop()
	}
	i := idx
	c.timers[idx] = time.AfterFunc(debounceDelay, func() {
		c.applyPending(i)
	})
	return nil
}

func (c *Controller) applyPending(index int) {
	c.mu.Lock()
	percent, ok := c.pending[index]
	if !ok {
		c.mu.Unlock()
		return
	}
	delete(c.pending, index)
	delete(c.timers, index)
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.mu.Unlock()

	if err := hwmon.SetPWM(chip, index, percent); err != nil {
		return
	}
	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		return
	}
	if onChange != nil {
		onChange(fansFromStatus(status, cfg))
	}
}

// SetMax sets all writable fans to 100%.
func (c *Controller) SetMax() error {
	c.mu.Lock()
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.mu.Unlock()

	if err := hwmon.SetMax(chip); err != nil {
		return err
	}
	return c.emit(chip, cfg, onChange)
}

// SetAuto returns writable fans to firmware auto control.
func (c *Controller) SetAuto() error {
	c.mu.Lock()
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.mu.Unlock()

	if err := hwmon.SetAuto(chip); err != nil {
		return err
	}
	return c.emit(chip, cfg, onChange)
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
