.PHONY: all build desktop doubletake doubletake-ctl doubletake-test-receiver doubletake-release doubletake-ctl-release doubletake-test-receiver-release manpages-release deb install install-files install-desktop-files install-man uninstall test clean

PREFIX ?= /usr/local
DATADIR ?= $(PREFIX)/share
MANDIR ?= $(DATADIR)/man
DEB_VERSION ?= 0.0.0~dev
DEB_ARCH ?= $(shell dpkg --print-architecture 2>/dev/null)
DESKTOP_ID := io.github.CompilingChaos.DoubleTake

all: doubletake doubletake-ctl doubletake-test-receiver

build: all

desktop:
	CGO_ENABLED=1 go build -tags desktop -o bin/doubletake-gui ./cmd/doubletake-gui

doubletake:
	go build -o bin/doubletake ./cmd/doubletake

doubletake-ctl:
	go build -o bin/doubletake-ctl ./cmd/doubletake-ctl

doubletake-test-receiver:
	go build -o bin/doubletake-test-receiver ./cmd/doubletake-test-receiver

doubletake-release:
	CGO_ENABLED=0 go build -ldflags='-s -w -extldflags=-static' -o doubletake ./cmd/doubletake

doubletake-ctl-release:
	CGO_ENABLED=0 go build -ldflags='-s -w -extldflags=-static' -o doubletake-ctl ./cmd/doubletake-ctl

doubletake-test-receiver-release:
	CGO_ENABLED=0 go build -ldflags='-s -w -extldflags=-static' -o doubletake-test-receiver ./cmd/doubletake-test-receiver

manpages-release:
	tar -czf doubletake-manpages.tar.gz -C man man1

test:
	go test ./...

install: all desktop
	$(MAKE) install-files

install-files: install-man install-desktop-files
	install -m 755 bin/doubletake bin/doubletake-ctl bin/doubletake-test-receiver "$(DESTDIR)$(PREFIX)/bin/"

install-desktop-files:
	install -D -m 755 bin/doubletake-gui "$(DESTDIR)$(PREFIX)/bin/doubletake-gui"
	install -D -m 644 cmd/doubletake-gui/icon-ui.png "$(DESTDIR)$(DATADIR)/icons/hicolor/128x128/apps/$(DESKTOP_ID).png"
	desktop-file-install --dir="$(DESTDIR)$(DATADIR)/applications" \
		--set-key=Exec --set-value='"$(PREFIX)/bin/doubletake-gui"' \
		--set-key=TryExec --set-value="$(PREFIX)/bin/doubletake-gui" \
		packaging/linux/$(DESKTOP_ID).desktop

install-man:
	install -d "$(DESTDIR)$(MANDIR)/man1"
	install -m 644 man/man1/doubletake.1 man/man1/doubletake-ctl.1 \
		man/man1/doubletake-test-receiver.1 "$(DESTDIR)$(MANDIR)/man1/"

deb: all desktop
	@set -eu; \
	dpkg --validate-version "$(DEB_VERSION)"; \
	stage=$$(mktemp -d); \
	trap 'rm -rf "$$stage"' EXIT; \
	$(MAKE) PREFIX=/usr DATADIR=/usr/share MANDIR=/usr/share/man DESTDIR="$$stage" install-files; \
	install -d "$$stage/DEBIAN" "$$stage/usr/share/doc/doubletake"; \
	install -m 644 LICENSE "$$stage/usr/share/doc/doubletake/copyright"; \
	install -m 644 COPYING.GPL "$$stage/usr/share/doc/doubletake/"; \
	sed -e 's/@VERSION@/$(DEB_VERSION)/' -e 's/@ARCH@/$(DEB_ARCH)/' packaging/linux/control > "$$stage/DEBIAN/control"; \
	mkdir -p artifacts; \
	dpkg-deb --root-owner-group --build "$$stage" "artifacts/doubletake-linux-$(DEB_ARCH).deb"

uninstall:
	rm -f "$(DESTDIR)$(PREFIX)/bin/doubletake" "$(DESTDIR)$(PREFIX)/bin/doubletake-ctl" \
		"$(DESTDIR)$(PREFIX)/bin/doubletake-test-receiver" "$(DESTDIR)$(PREFIX)/bin/doubletake-gui"
	rm -f "$(DESTDIR)$(DATADIR)/applications/$(DESKTOP_ID).desktop" \
		"$(DESTDIR)$(DATADIR)/icons/hicolor/128x128/apps/$(DESKTOP_ID).png"
	rm -f "$(DESTDIR)$(MANDIR)/man1/doubletake.1" "$(DESTDIR)$(MANDIR)/man1/doubletake-ctl.1" \
		"$(DESTDIR)$(MANDIR)/man1/doubletake-test-receiver.1"

clean:
	rm -rf bin/
	go clean -testcache
