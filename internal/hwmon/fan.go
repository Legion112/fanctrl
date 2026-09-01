package hwmon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	EnableManual = 1
	EnableAuto   = 2
)

var ErrReadOnlyPWM = fmt.Errorf("PWM controls are read-only; install the ASRock nct6683 DKMS driver (see README)")

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

	if err := os.WriteFile(pwmPath, []byte(strconv.Itoa(pwmValue)), 0); err != nil {
		return fmt.Errorf("set pwm%d to %d: %w", index, pwmValue, err)
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
		enablePath := filepath.Join(chip.Path, fmt.Sprintf("pwm%d_enable", fan.Index))
		if _, err := os.Stat(enablePath); err != nil {
			continue
		}
		if !isWritable(enablePath) {
			readOnly++
			continue
		}
		if err := os.WriteFile(enablePath, []byte(strconv.Itoa(EnableAuto)), 0); err != nil {
			return fmt.Errorf("set pwm%d auto mode: %w", fan.Index, err)
		}
		updated++
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
