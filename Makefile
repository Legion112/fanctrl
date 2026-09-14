PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
CONFIG_SRC = configs/headers.x570-creator.yaml
CONFIG_DST = /etc/fanctl/headers.yaml

.PHONY: build install install-config

build:
	go build -o fanctl ./cmd/fanctl

install: build install-config
	install -D -m 755 fanctl $(BINDIR)/fanctl

install-config:
	install -D -m 644 $(CONFIG_SRC) $(CONFIG_DST)
