package control

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/legion/fanctl/internal/config"
	"github.com/legion/fanctl/internal/hwmon"
)

const (
	defaultChip = "nct6683"
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
	c.mu.Unlock()

	if err := hwmon.SetMax(chip); err != nil {
		log.Printf("SetMax failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return err
	}
	log.Printf("SetMax ok in %s", time.Since(start).Round(time.Millisecond))
	return c.emit(chip, cfg, onChange)
}

// SetAuto returns writable fans to firmware auto control.
func (c *Controller) SetAuto() error {
	start := time.Now()
	c.mu.Lock()
	chip := c.chip
	onChange := c.onChange
	cfg := c.cfg
	c.mu.Unlock()

	if err := hwmon.SetAuto(chip); err != nil {
		log.Printf("SetAuto failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return err
	}
	log.Printf("SetAuto ok in %s", time.Since(start).Round(time.Millisecond))
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
