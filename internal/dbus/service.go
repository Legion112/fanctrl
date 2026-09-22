package dbus

import (
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/legion/fanctl/internal/control"
)

const (
	BusName    = "org.fanctl.Control"
	ObjectPath = "/org/fanctl/Control"
	Interface  = "org.fanctl.Control"
)

// Service exports org.fanctl.Control on the system bus.
type Service struct {
	conn       *dbus.Conn
	controller *control.Controller
}

// NewService wires the D-Bus service to a controller.
func NewService(conn *dbus.Conn, controller *control.Controller) *Service {
	s := &Service{conn: conn, controller: controller}
	controller.SetChangeHandler(func(fans []control.FanInfo) {
		s.emitFansChanged(fans)
	})
	return s
}

// Export registers methods on the connection.
func (s *Service) Export() error {
	return s.conn.Export(s, dbus.ObjectPath(ObjectPath), Interface)
}

func (s *Service) emitFansChanged(fans []control.FanInfo) {
	if s.conn == nil {
		return
	}
	_ = s.conn.Emit(dbus.ObjectPath(ObjectPath), Interface+".FansChanged", fans)
}

// GetFans returns the current fan list.
func (s *Service) GetFans() ([]control.FanInfo, *dbus.Error) {
	fans, err := s.controller.GetFans()
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	return fans, nil
}

// SetPercent sets one fan PWM percent (applied immediately in the controller).
func (s *Service) SetPercent(index uint32, percent byte) *dbus.Error {
	if err := s.controller.SetPercent(index, percent); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// SetMax sets all writable fans to 100%.
func (s *Service) SetMax() *dbus.Error {
	if err := s.controller.SetMax(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// SetAuto returns writable fans to firmware automatic control.
func (s *Service) SetAuto() *dbus.Error {
	if err := s.controller.SetAuto(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// SaveState snapshots the current fan speeds as the profile restored at boot.
func (s *Service) SaveState() *dbus.Error {
	if err := s.controller.SaveState(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// RestoreState re-applies the saved profile now.
func (s *Service) RestoreState() *dbus.Error {
	if _, err := s.controller.RestoreState(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// AcquireName requests the well-known bus name.
func AcquireName(conn *dbus.Conn) error {
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("bus name %s already taken", BusName)
	}
	return nil
}
