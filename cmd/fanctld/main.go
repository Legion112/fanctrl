package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/legion/fanctl/internal/control"
	dbussvc "github.com/legion/fanctl/internal/dbus"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("fanctld: ")

	controller, err := control.New()
	if err != nil {
		log.Fatalf("init: %v", err)
	}

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

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		controller.EmitCurrent()
		for range ticker.C {
			controller.EmitCurrent()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutting down")
}
