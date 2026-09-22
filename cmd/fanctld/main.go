package main

import (
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/legion/fanctl/internal/control"
	dbussvc "github.com/legion/fanctl/internal/dbus"
)

const (
	// chipWait bounds how long to wait for the nct6683 module at boot. Exiting
	// instead would burn systemd's start limit (5 failures in 10s) and leave the
	// unit permanently failed, which also blocks D-Bus activation.
	chipWait      = 60 * time.Second
	chipRetryEach = 1 * time.Second
	chipLogEach   = 5 * time.Second

	// pollInterval is how often the daemon pushes FansChanged.
	pollInterval = 2 * time.Second
	// verifyWindow is how long after startup to re-assert the saved profile.
	// The EC is still finishing its own init for the first few seconds, which is
	// the window where a restore write can be silently reverted.
	verifyWindow = 15 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("fanctld: ")

	controller, err := waitForController()
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	// Restore before announcing on the bus: a few milliseconds of sysfs writes
	// buys correct fan speeds as early in boot as possible.
	restoreSaved(controller)

	conn, err := dbus.SystemBus()
	if err != nil {
		log.Fatalf("system bus: %v", err)
	}

	service := dbussvc.NewService(conn, controller)
	if err := service.Export(); err != nil {
		log.Fatalf("export dbus: %v", err)
	}
	if err := dbussvc.AcquireName(conn); err != nil {
		log.Fatalf("acquire name: %v", err)
	}

	log.Printf("ready on %s", dbussvc.BusName)

	go poll(controller)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutting down")
}

// waitForController retries chip discovery, because at boot the nct6683 module
// may not be loaded yet. control.New keeps its fail-fast contract for the CLI.
func waitForController() (*control.Controller, error) {
	deadline := time.Now().Add(chipWait)
	lastLog := time.Time{}
	for {
		controller, err := control.New()
		if err == nil {
			return controller, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		if time.Since(lastLog) >= chipLogEach {
			log.Printf("waiting for hwmon chip: %v", err)
			lastLog = time.Now()
		}
		time.Sleep(chipRetryEach)
	}
}

func restoreSaved(controller *control.Controller) {
	applied, err := controller.RestoreState()
	switch {
	case errors.Is(err, control.ErrNoProfile):
		log.Printf("no saved profile; leaving fans as the firmware set them")
	case err != nil:
		log.Printf("restore failed: %v", err)
	default:
		log.Printf("restored %d fans from the saved profile", applied)
	}
}

func poll(controller *control.Controller) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	verifyUntil := time.Now().Add(verifyWindow)
	controller.EmitCurrent()
	for range ticker.C {
		// Re-assert the profile for a short while after boot, in case the EC
		// reverted it. Plan skips fans already at the saved value, so once
		// things settle this costs nothing, and UserTouched ends it early the
		// moment the user changes anything.
		if time.Now().Before(verifyUntil) && !controller.UserTouched() {
			if n, err := controller.VerifySaved(); err != nil {
				if !errors.Is(err, control.ErrNoProfile) {
					log.Printf("verify failed: %v", err)
				}
				verifyUntil = time.Time{}
			} else if n > 0 {
				log.Printf("verify re-applied %d fans", n)
			}
		}
		controller.EmitCurrent()
	}
}
