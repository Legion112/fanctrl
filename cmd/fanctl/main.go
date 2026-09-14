package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/legion/fanctl/internal/config"
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
	case "init-config":
		runInitConfig(os.Args[2:])
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
	configPath := fs.String("config", "", "path to headers.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Find(*configPath)
	if err != nil {
		fatal(err)
	}

	chip, err := hwmon.FindChip(*chipName)
	if err != nil {
		fatal(err)
	}

	status, err := hwmon.ReadStatus(chip)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("chip: %s (%s)\n", status.Chip.Name, status.Chip.Path)
	if cfg != nil && cfg.Path != "" {
		fmt.Printf("headers: %s\n", cfg.Path)
	}
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

		header, ok := cfg.ByIndex(f.Index)
		if ok && header.Name != "" {
			if header.Note != "" {
				fmt.Printf("  fan%d  %s  (%s)\n", f.Index, header.Name, header.Note)
			} else {
				fmt.Printf("  fan%d  %s\n", f.Index, header.Name)
			}
			fmt.Printf("        %d RPM  pwm=%d (%d%%)  control=%s  %s\n",
				f.RPM, f.PWM, f.Percent, mode, write)
			continue
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
	configPath := fs.String("config", "", "path to headers.yaml")
	pwm := fs.Int("pwm", 0, "PWM index (1-based, matches sysfs pwmN)")
	name := fs.String("name", "", "header name from headers.yaml (e.g. CPU_FAN1)")
	percent := fs.Int("pct", -1, "target fan speed percent (0-100)")
	_ = fs.Parse(args)

	if *percent < 0 {
		fatal(fmt.Errorf("set requires -pct PERCENT (0-100)"))
	}
	if (*pwm <= 0 && *name == "") || (*pwm > 0 && *name != "") {
		fatal(fmt.Errorf("set requires exactly one of -pwm N or -name NAME"))
	}

	index := *pwm
	if *name != "" {
		cfg, err := config.Find(*configPath)
		if err != nil {
			fatal(err)
		}
		index, err = cfg.IndexByName(*name)
		if err != nil {
			fatal(err)
		}
	}

	chip, err := hwmon.FindChip(*chipName)
	if err != nil {
		fatal(err)
	}
	if err := hwmon.SetPWM(chip, index, *percent); err != nil {
		fatal(err)
	}
	if *name != "" {
		fmt.Printf("pwm%d (%s) set to %d%%\n", index, *name, *percent)
	} else {
		fmt.Printf("pwm%d set to %d%%\n", index, *percent)
	}
}

func runAuto(args []string) {
	chip := openChip(args)
	if err := hwmon.SetAuto(chip); err != nil {
		fatal(err)
	}
	fmt.Println("writable PWM outputs returned to automatic firmware control")
}

func runInitConfig(args []string) {
	fs := flag.NewFlagSet("init-config", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite existing headers.yaml")
	_ = fs.Parse(args)

	path := config.DefaultSystemPath
	if _, err := os.Stat(path); err == nil && !*force {
		fatal(fmt.Errorf("%s already exists (use -force to overwrite)", path))
	} else if err != nil && !os.IsNotExist(err) {
		fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, config.ExampleX570CreatorYAML, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", path)
	fmt.Println("Edit names after matching RPM to headers (unplug one connector, re-run fanctl status).")
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
  set -name NAME -pct P
                      set by silk-screen name from headers.yaml
  auto                return writable PWM outputs to firmware control
  init-config         write example /etc/fanctl/headers.yaml (requires root)

Options:
  -chip NAME          hwmon chip name (default: %s)
  -config PATH        headers.yaml path (else FANCTL_CONFIG, then
                      /etc/fanctl/headers.yaml)

Write commands require root (sudo). If PWM files are read-only, install the
ASRock nct6683 DKMS driver documented in README.md.

Examples:
  %s status
  sudo %s init-config
  sudo %s max
  sudo %s set -pwm 4 -pct 100
  sudo %s set -name CPU_FAN1 -pct 70
  sudo %s auto
`, name, defaultChip, name, name, name, name, name, name)
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
