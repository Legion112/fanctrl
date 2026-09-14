PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
CONFIG_SRC = configs/headers.x570-creator.yaml
CONFIG_DST = /etc/fanctl/headers.yaml
DBUS_CONF_DST = /usr/share/dbus-1/system.d/org.fanctl.Control.conf
DBUS_SVC_DST = /usr/share/dbus-1/system-services/org.fanctl.Control.service
SYSTEMD_DST = /lib/systemd/system/fanctld.service
ICON_DST = /usr/share/icons/hicolor/scalable/status/fanctl-symbolic.svg
AUTOSTART_DST = /etc/xdg/autostart/org.fanctl.Gui.desktop

# sudo strips PATH; resolve cargo for the invoking user when present.
ifdef SUDO_USER
REAL_HOME := $(shell getent passwd $(SUDO_USER) | cut -d: -f6)
else
REAL_HOME := $(HOME)
endif
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
	install -D -m 644 packaging/org.fanctl.Gui.desktop $(AUTOSTART_DST)
	gtk-update-icon-cache -f /usr/share/icons/hicolor 2>/dev/null || true

deploy-gui: install install-gui
	@echo "Start fanctl-gui once (or log out/in) for the top-bar icon:"
	@echo "  fanctl-gui &"
