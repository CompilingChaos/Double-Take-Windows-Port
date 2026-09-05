.PHONY: all build doubletake doubletake-ctl doubletake-release doubletake-ctl-release manpages-release install install-man uninstall test clean

PREFIX ?= /usr/local
MANDIR ?= $(PREFIX)/share/man
EXEEXT ?=
BIN_DIR ?= bin

all: doubletake doubletake-ctl

build: all

doubletake:
	go build -o $(BIN_DIR)/doubletake$(EXEEXT) ./cmd/doubletake

doubletake-ctl:
	go build -o $(BIN_DIR)/doubletake-ctl$(EXEEXT) ./cmd/doubletake-ctl

doubletake-release:
	go build -ldflags='-s -w' -o doubletake$(EXEEXT) ./cmd/doubletake

doubletake-ctl-release:
	go build -ldflags='-s -w' -o doubletake-ctl$(EXEEXT) ./cmd/doubletake-ctl

manpages-release:
	tar -czf doubletake-manpages.tar.gz -C man man1

test:
	go test ./...

install: all install-man
	install -m 755 $(BIN_DIR)/doubletake$(EXEEXT) $(PREFIX)/bin/
	install -m 755 $(BIN_DIR)/doubletake-ctl$(EXEEXT) $(PREFIX)/bin/

install-man:
	install -d $(MANDIR)/man1
	install -m 644 man/man1/doubletake.1 $(MANDIR)/man1/
	install -m 644 man/man1/doubletake-ctl.1 $(MANDIR)/man1/

uninstall:
	rm -f $(PREFIX)/bin/doubletake
	rm -f $(PREFIX)/bin/doubletake-ctl
	rm -f $(MANDIR)/man1/doubletake.1
	rm -f $(MANDIR)/man1/doubletake-ctl.1

clean:
	rm -rf $(BIN_DIR)/
	rm -f doubletake doubletake.exe doubletake-ctl doubletake-ctl.exe
	go clean -testcache
