# based off of the Makefile for aerc:
# https://git.sr.ht/~rjarry/aerc/tree/master/item/Makefile

.POSIX:
.SUFFIXES:
.SUFFIXES: .1 .1.scd .5 .5.scd

VERSION?=`git describe --tags --dirty 2>/dev/null || echo 0.0.0`
VPATH=doc
PREFIX?=/usr/local
BINDIR?=$(PREFIX)/bin
SHAREDIR?=$(PREFIX)/share
SYSCONFDIR?=/etc
MANDIR?=$(PREFIX)/share/man
GO?=go
GOFLAGS?=
LDFLAGS+=-X main.Version=$(VERSION) -s -w
RM?=rm -f

GOSRC!=find * -name '*.go'
GOSRC+=go.mod go.sum

DOCS := \
	gnss-share.1 \
	gnss-share.conf.5

all: gnss-share $(DOCS)

gnss-share: $(GOSRC)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o gnss-share ./cmd/gnss-share

.PHONY: fmt
fmt:
	gofmt -w .

# NOTE: most of the skipped staticcheck tests are from the default, SA1019
# skips the ioutil deprecation warning since some distros (Debian) are only on
# Go 1.15, which was before some ioutil functionality was moved into other
# modules (e.g. os.)
# See: https://staticcheck.io/docs/configuration
test:
	@if [ `gofmt -l . | wc -l` -ne 0 ]; then \
		gofmt -d .; \
		echo "ERROR: source files need reformatting with gofmt"; \
		exit 1; \
	fi
	@staticcheck -checks all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023,-SA1019 ./...
	@go test -race -count=1 ./...

.1.scd.1:
	scdoc < $< > $@

.5.scd.5:
	scdoc < $< > $@

doc: $(DOCS)

clean:
	$(RM) $(DOCS) gnss-share

install: gnss-share $(DOCS)
	mkdir -m755 -p $(DESTDIR)$(BINDIR)
	install -m755 gnss-share $(DESTDIR)$(BINDIR)/
	mkdir -m755 -p $(DESTDIR)$(SYSCONFDIR)
	install -m644 gnss-share.conf $(DESTDIR)$(BINDIR)/
	install -Dm644 gnss-share.1 -t $(DESTDIR)$(MANDIR)/man1/
	install -Dm644 gnss-share.conf.5 -t $(DESTDIR)$(MANDIR)/man5/

.PHONY: checkinstall
checkinstall:
	test -e $(DESTDIR)$(BINDIR)/gnss-share
	test -e $(DESTDIR)$(BINDIR)/gnss-share.conf 
	test -e $(DESTDIR)$(MANDIR)/man1/gnss-share.1
	test -e $(DESTDIR)$(MANDIR)/man5/gnss-share.conf.5

RMDIR_IF_EMPTY:=sh -c '! [ -d $$0 ] || ls -1qA $$0 | grep -q . || rmdir $$0'

uninstall:
	$(RM) $(DESTDIR)$(BINDIR)/gnss-share
	$(RM) $(DESTDIR)$(BINDIR)/gnss-share.conf 
	$(RM) $(DESTDIR)$(MANDIR)/man1/gnss-share.1
	$(RM) $(DESTDIR)$(MANDIR)/man5/gnss-share.conf.5

.PHONY: all doc clean install uninstall test
