package hwmon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const defaultSysfsRoot = "/sys/class/hwmon"

// Chip represents a discovered hwmon device.
type Chip struct {
	Path string
	Name string
}

// FindChip locates the first hwmon device with the given name.
func FindChip(name string) (*Chip, error) {
	return FindChipIn(defaultSysfsRoot, name)
}

// FindChipIn locates a hwmon device under root (for tests).
func FindChipIn(root, name string) (*Chip, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read hwmon root %q: %w", root, err)
	}

	var matches []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "hwmon") {
			continue
		}
		chipPath := filepath.Join(root, entry.Name())
		chipName, err := os.ReadFile(filepath.Join(chipPath, "name"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(chipName)) == name {
			matches = append(matches, chipPath)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("hwmon chip %q not found under %s (is nct6683 loaded with force=1?)", name, root)
	}

	sort.Strings(matches)
	return &Chip{Path: matches[0], Name: name}, nil
}

// Fan describes one fan channel.
type Fan struct {
	Index   int
	RPM     int
	PWM     int
	Percent int
	Enable  *int
	Writable bool
}

// Temperature describes one temperature sensor.
type Temperature struct {
	Index int
	Label string
	C     float64
}

// Status holds chip sensor readings.
type Status struct {
	Chip         Chip
	Fans         []Fan
	Temperatures []Temperature
}

// ReadStatus reads fans and temperatures from the chip.
func ReadStatus(chip *Chip) (*Status, error) {
	fans, err := readFans(chip.Path)
	if err != nil {
		return nil, err
	}
	temps, err := readTemperatures(chip.Path)
	if err != nil {
		return nil, err
	}
	return &Status{
		Chip:         *chip,
		Fans:         fans,
		Temperatures: temps,
	}, nil
}

func readFans(chipPath string) ([]Fan, error) {
	entries, err := os.ReadDir(chipPath)
	if err != nil {
		return nil, fmt.Errorf("read chip dir: %w", err)
	}

	indexes := map[int]struct{}{}
	for _, entry := range entries {
		var idx int
		if _, err := fmt.Sscanf(entry.Name(), "fan%d_input", &idx); err != nil {
			continue
		}
		indexes[idx] = struct{}{}
	}
	for _, entry := range entries {
		var idx int
		if _, err := fmt.Sscanf(entry.Name(), "pwm%d", &idx); err != nil {
			continue
		}
		indexes[idx] = struct{}{}
	}

	var fans []Fan
	for idx := range indexes {
		fan := Fan{Index: idx}
		if rpm, err := readInt(filepath.Join(chipPath, fmt.Sprintf("fan%d_input", idx))); err == nil {
			fan.RPM = rpm
		}
		if pwm, err := readInt(filepath.Join(chipPath, fmt.Sprintf("pwm%d", idx))); err == nil {
			fan.PWM = pwm
			fan.Percent = PWMToPercent(pwm)
		}
		enablePath := filepath.Join(chipPath, fmt.Sprintf("pwm%d_enable", idx))
		if enable, err := readInt(enablePath); err == nil {
			fan.Enable = &enable
		}
		pwmPath := filepath.Join(chipPath, fmt.Sprintf("pwm%d", idx))
		fan.Writable = isWritable(pwmPath)
		fans = append(fans, fan)
	}

	sort.Slice(fans, func(i, j int) bool { return fans[i].Index < fans[j].Index })
	return fans, nil
}

func readTemperatures(chipPath string) ([]Temperature, error) {
	entries, err := os.ReadDir(chipPath)
	if err != nil {
		return nil, fmt.Errorf("read chip dir: %w", err)
	}

	var temps []Temperature
	for _, entry := range entries {
		var idx int
		if _, err := fmt.Sscanf(entry.Name(), "temp%d_input", &idx); err != nil {
			continue
		}
		milli, err := readInt(filepath.Join(chipPath, fmt.Sprintf("temp%d_input", idx)))
		if err != nil {
			continue
		}
		temp := Temperature{Index: idx, C: float64(milli) / 1000}
		labelPath := filepath.Join(chipPath, fmt.Sprintf("temp%d_label", idx))
		if label, err := os.ReadFile(labelPath); err == nil {
			temp.Label = strings.TrimSpace(string(label))
		}
		temps = append(temps, temp)
	}

	sort.Slice(temps, func(i, j int) bool { return temps[i].Index < temps[j].Index })
	return temps, nil
}

func readInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func isWritable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	mode := info.Mode().Perm()
	return mode&0200 != 0
}
