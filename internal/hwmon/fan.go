package hwmon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// The ASRock nct6683 DKMS driver uses 0/1 on pwmN_enable (not standard hwmon 1=manual, 2=auto).
	EnableManual = 1
	EnableAuto   = 0
)

// ControlModeLabel formats pwmN_enable for display.
func ControlModeLabel(enable int) string {
	switch enable {
	case EnableManual:
		return "manual"
	case EnableAuto:
		return "auto"
	default:
		return fmt.Sprintf("mode=%d", enable)
	}
}

var ErrReadOnlyPWM = fmt.Errorf("PWM controls are read-only; install the ASRock nct6683 DKMS driver (see README)")

// ErrNoEnableFile reports a header without a pwmN_enable control, so firmware
// automatic mode cannot be selected for it.
var ErrNoEnableFile = errors.New("pwmN_enable not present")

// PercentToPWM maps 0-100 percent to 0-255 PWM duty cycle.
func PercentToPWM(percent int) (int, error) {
	if percent < 0 || percent > 100 {
		return 0, fmt.Errorf("percent must be 0-100, got %d", percent)
	}
	return (percent*255 + 50) / 100, nil
}

// PWMToPercent maps 0-255 PWM to 0-100 percent.
func PWMToPercent(pwm int) int {
	if pwm <= 0 {
		return 0
	}
	if pwm >= 255 {
		return 100
	}
	return (pwm*100 + 127) / 255
}

// SetPWM sets one fan header to manual mode at the given percent.
func SetPWM(chip *Chip, index int, percent int) error {
	if err := requireRoot(); err != nil {
		return err
	}
	pwmValue, err := PercentToPWM(percent)
	if err != nil {
		return err
	}
	return SetPWMRaw(chip, index, pwmValue)
}

// SetPWMRaw sets one fan header to manual mode at a raw 0-255 duty cycle.
// Restore uses this directly: percent round-trips lose a step (pwm 100 -> 39% -> 99).
func SetPWMRaw(chip *Chip, index int, pwm int) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if pwm < 0 || pwm > 255 {
		return fmt.Errorf("pwm must be 0-255, got %d", pwm)
	}

	pwmPath := filepath.Join(chip.Path, fmt.Sprintf("pwm%d", index))
	enablePath := filepath.Join(chip.Path, fmt.Sprintf("pwm%d_enable", index))

	if _, err := os.Stat(pwmPath); err != nil {
		return fmt.Errorf("pwm%d not found under %s", index, chip.Path)
	}
	if !isWritable(pwmPath) {
		return ErrReadOnlyPWM
	}

	if _, err := os.Stat(enablePath); err == nil {
		if !isWritable(enablePath) {
			return ErrReadOnlyPWM
		}
		if err := os.WriteFile(enablePath, []byte(strconv.Itoa(EnableManual)), 0); err != nil {
			return fmt.Errorf("set pwm%d manual mode: %w", index, err)
		}
	}

	if err := os.WriteFile(pwmPath, []byte(strconv.Itoa(pwm)), 0); err != nil {
		return fmt.Errorf("set pwm%d to %d: %w", index, pwm, err)
	}
	return nil
}

// SetMax sets all writable PWM outputs to 100%.
func SetMax(chip *Chip) error {
	if err := requireRoot(); err != nil {
		return err
	}

	status, err := ReadStatus(chip)
	if err != nil {
		return err
	}

	var updated int
	var readOnly int
	for _, fan := range status.Fans {
		if !fan.Writable {
			readOnly++
			continue
		}
		if err := SetPWM(chip, fan.Index, 100); err != nil {
			return err
		}
		updated++
	}

	if updated == 0 {
		if readOnly > 0 {
			return ErrReadOnlyPWM
		}
		return fmt.Errorf("no pwm controls found under %s", chip.Path)
	}
	return nil
}

// SetAutoIndex returns one fan header to firmware automatic control.
// Reports ErrNoEnableFile when pwmN_enable is absent, ErrReadOnlyPWM when it is not writable.
func SetAutoIndex(chip *Chip, index int) error {
	if err := requireRoot(); err != nil {
		return err
	}

	enablePath := filepath.Join(chip.Path, fmt.Sprintf("pwm%d_enable", index))
	if _, err := os.Stat(enablePath); err != nil {
		return ErrNoEnableFile
	}
	if !isWritable(enablePath) {
		return ErrReadOnlyPWM
	}
	if err := os.WriteFile(enablePath, []byte(strconv.Itoa(EnableAuto)), 0); err != nil {
		return fmt.Errorf("set pwm%d auto mode: %w", index, err)
	}
	return nil
}

// SetAuto returns all fans with pwmN_enable to firmware automatic control.
func SetAuto(chip *Chip) error {
	if err := requireRoot(); err != nil {
		return err
	}

	status, err := ReadStatus(chip)
	if err != nil {
		return err
	}

	var updated int
	var readOnly int
	for _, fan := range status.Fans {
		err := SetAutoIndex(chip, fan.Index)
		switch {
		case err == nil:
			updated++
		case errors.Is(err, ErrNoEnableFile):
			continue
		case errors.Is(err, ErrReadOnlyPWM):
			readOnly++
		default:
			return err
		}
	}

	if updated == 0 {
		if readOnly > 0 {
			return ErrReadOnlyPWM
		}
		return fmt.Errorf("no pwmN_enable controls found under %s", chip.Path)
	}
	return nil
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root privileges required; run with sudo")
	}
	return nil
}
