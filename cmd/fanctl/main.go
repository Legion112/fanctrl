package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/legion/fanctl/internal/hwmon"
)

const defaultChip = "nct6683"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "status":
		runStatus(os.Args[2:])
	case "max":
		runMax(os.Args[2:])
	case "set":
		runSet(os.Args[2:])
	case "auto":
		runAuto(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	chipName := fs.String("chip", defaultChip, "hwmon chip name")
	_ = fs.Parse(args)

	chip, err := hwmon.FindChip(*chipName)
	if err != nil {
		fatal(err)
	}

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("chip: %s (%s)\n", status.Chip.Name, status.Chip.Path)
	fmt.Println()

	if len(status.Temperatures) > 0 {
		fmt.Println("temperatures:")
		for _, t := range status.Temperatures {
			label := t.Label
			if label == "" {
				label = fmt.Sprintf("temp%d", t.Index)
			}
			fmt.Printf("  %s: %.1f°C\n", label, t.C)
		}
		fmt.Println()
	}

	fmt.Println("fans:")
	for _, f := range status.Fans {
		mode := "-"
		if f.Enable != nil {
			mode = hwmon.ControlModeLabel(*f.Enable)
		}
		write := "read-only"
		if f.Writable {
			write = "writable"
		}
		fmt.Printf("  fan%d: %d RPM  pwm=%d (%d%%)  control=%s  %s\n",
			f.Index, f.RPM, f.PWM, f.Percent, mode, write)
	}
}

func runMax(args []string) {
	chip := openChip(args)
	if err := hwmon.SetMax(chip); err != nil {
		fatal(err)
	}
	fmt.Println("all writable PWM outputs set to 100%")
}

func runSet(args []string) {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	chipName := fs.String("chip", defaultChip, "hwmon chip name")
	pwm := fs.Int("pwm", 0, "PWM index (1-based, matches sysfs pwmN)")
	percent := fs.Int("pct", -1, "target fan speed percent (0-100)")
	_ = fs.Parse(args)

	if *pwm <= 0 {
		fatal(fmt.Errorf("set requires -pwm N (1-based index matching sysfs pwmN)"))
	}
	if *percent < 0 {
		fatal(fmt.Errorf("set requires -pct PERCENT (0-100)"))
	}

	chip, err := hwmon.FindChip(*chipName)
	if err != nil {
		fatal(err)
	}
	if err := hwmon.SetPWM(chip, *pwm, *percent); err != nil {
		fatal(err)
	}
	fmt.Printf("pwm%d set to %d%%\n", *pwm, *percent)
}

func runAuto(args []string) {
	chip := openChip(args)
	if err := hwmon.SetAuto(chip); err != nil {
		fatal(err)
	}
	fmt.Println("writable PWM outputs returned to automatic firmware control")
}

func openChip(args []string) *hwmon.Chip {
	fs := flag.NewFlagSet("cmd", flag.ExitOnError)
	chipName := fs.String("chip", defaultChip, "hwmon chip name")
	_ = fs.Parse(args)

	chip, err := hwmon.FindChip(*chipName)
	if err != nil {
		fatal(err)
	}
	return chip
}

func usage() {
	name := "fanctl"
	if len(os.Args) > 0 {
		name = filepathBase(os.Args[0])
	}
	fmt.Fprintf(os.Stderr, `Usage: %s <command> [options]

Commands:
  status              show temperatures, fan RPM, and PWM values
  max                 set all writable PWM outputs to 100%%
  set -pwm N -pct P   set one PWM header to P%% (0-100)
  auto                return writable PWM outputs to firmware control

Options:
  -chip NAME          hwmon chip name (default: %s)

Write commands require root (sudo). If PWM files are read-only, install the
ASRock nct6683 DKMS driver documented in README.md.

Examples:
  %s status
  sudo %s max
  sudo %s set -pwm 4 -pct 100
  sudo %s auto
`, name, defaultChip, name, name, name, name)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "fanctl: %v\n", err)
	os.Exit(1)
}

func filepathBase(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
