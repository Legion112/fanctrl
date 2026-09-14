PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
CONFIG_SRC = configs/headers.x570-creator.yaml
CONFIG_DST = /etc/fanctl/headers.yaml
DBUS_CONF_DST = /usr/share/dbus-1/system.d/org.fanctl.Control.conf
DBUS_SVC_DST = /usr/share/dbus-1/system-services/org.fanctl.Control.service
SYSTEMD_DST = /lib/systemd/system/fanctld.service
USER_SYSTEMD_DST = /usr/lib/systemd/user/fanctl-gui.service
ICON_DST = /usr/share/icons/hicolor/scalable/status/fanctl-symbolic.svg
# Legacy path; remove on install so xdg-autostart does not double-start with systemd.
OLD_AUTOSTART = /etc/xdg/autostart/org.fanctl.Gui.desktop

# sudo strips PATH; resolve cargo for the invoking user when present.
ifdef SUDO_USER
REAL_USER := $(SUDO_USER)
REAL_HOME := $(shell getent passwd $(SUDO_USER) | cut -d: -f6)
else
REAL_USER := $(USER)
REAL_HOME := $(HOME)
endif
REAL_UID := $(shell id -u $(REAL_USER) 2>/dev/null || echo)
CARGO ?= $(firstword $(wildcard $(shell command -v cargo 2>/dev/null)) $(wildcard $(REAL_HOME)/.cargo/bin/cargo) cargo)

.PHONY: build build-gui install install-config install-daemon install-gui deploy-gui test

build:
	go build -o fanctl ./cmd/fanctl
	go build -o fanctld ./cmd/fanctld

build-gui:
	@if ! command -v "$(CARGO)" >/dev/null 2>&1 && [ ! -x "$(CARGO)" ]; then \
		echo "cargo not found (sudo hides ~/.cargo/bin). Install Rust or run: make build-gui && sudo make install-gui"; \
		exit 127; \
	fi
	@# Build as the real user so target/ is not root-owned when invoked via sudo make.
	@if [ -n "$(SUDO_USER)" ]; then \
		sudo -u "$(SUDO_USER)" -H env HOME="$(REAL_HOME)" PATH="$(REAL_HOME)/.cargo/bin:$$PATH" \
			"$(CARGO)" build --release --manifest-path gui/Cargo.toml; \
	else \
		"$(CARGO)" build --release --manifest-path gui/Cargo.toml; \
	fi

test:
	go test ./...

install-config:
	install -D -m 644 $(CONFIG_SRC) $(CONFIG_DST)

install-daemon: build install-config
	install -D -m 755 fanctld $(BINDIR)/fanctld
	install -D -m 644 dbus/org.fanctl.Control.conf $(DBUS_CONF_DST)
	install -D -m 644 dbus/org.fanctl.Control.service $(DBUS_SVC_DST)
	install -D -m 644 systemd/fanctld.service $(SYSTEMD_DST)
	systemctl daemon-reload
	systemctl enable --now fanctld.service

install: build install-config install-daemon
	install -D -m 755 fanctl $(BINDIR)/fanctl

install-gui: build-gui
	install -D -m 755 gui/target/release/fanctl-gui $(BINDIR)/fanctl-gui
	install -D -m 644 gui/icons/fanctl-symbolic.svg $(ICON_DST)
	install -D -m 644 systemd/fanctl-gui.service $(USER_SYSTEMD_DST)
	rm -f $(OLD_AUTOSTART)
	gtk-update-icon-cache -f /usr/share/icons/hicolor 2>/dev/null || true
	@if [ -z "$(REAL_UID)" ] || [ ! -d "/run/user/$(REAL_UID)" ]; then \
		echo "fanctl-gui installed; enable when logged into a graphical session:"; \
		echo "  systemctl --user daemon-reload"; \
		echo "  systemctl --user enable --now fanctl-gui.service"; \
	elif [ -n "$(SUDO_USER)" ]; then \
		sudo -u "$(SUDO_USER)" XDG_RUNTIME_DIR="/run/user/$(REAL_UID)" \
			systemctl --user daemon-reload; \
		sudo -u "$(SUDO_USER)" XDG_RUNTIME_DIR="/run/user/$(REAL_UID)" \
			systemctl --user enable --now fanctl-gui.service; \
	else \
		systemctl --user daemon-reload; \
		systemctl --user enable --now fanctl-gui.service; \
	fi
	@echo "Logs: journalctl --user -u fanctl-gui -f"

deploy-gui: install install-gui
	@echo "Tray service: systemctl --user status fanctl-gui"
	@echo "Logs:         journalctl --user -u fanctl-gui -f"
