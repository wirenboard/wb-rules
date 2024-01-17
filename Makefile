.PHONY: all clean

PREFIX = /usr
DEB_TARGET_ARCH ?= armhf

ifeq ($(DEB_TARGET_ARCH),armel)
GO_ENV := GOARCH=arm GOARM=5 CC_FOR_TARGET=arm-linux-gnueabi-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),armhf)
GO_ENV := GOARCH=arm GOARM=6 CC_FOR_TARGET=arm-linux-gnueabihf-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),arm64)
GO_ENV := GOARCH=arm64 GOARM=6 CC_FOR_TARGET=aarch64-linux-gnu-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),amd64)
GO_ENV := GOARCH=amd64 CC=x86_64-linux-gnu-gcc
endif
ifeq ($(DEB_TARGET_ARCH),i386)
GO_ENV := GOARCH=386 CC=i586-linux-gnu-gcc
endif

GO_ENV := GO111MODULE=on $(GO_ENV)

GO_FLAGS=-ldflags "-w"

all: clean wb-rules

clean:
	rm -rf wb-rules

amd64:
	$(MAKE) DEB_TARGET_ARCH=amd64

test:
	cp amd64.wbgo.so wbrules/wbgo.so
	CC=x86_64-linux-gnu-gcc go test -v -trimpath -ldflags="-s -w" ./wbrules

wb-rules: main.go wbrules/*.go
	$(GO_ENV) go build -trimpath -ldflags "-w -X main.version=`git describe --tags --always --dirty`"

install:
	mkdir -p $(DESTDIR)/etc/init.d/
	mkdir -p $(DESTDIR)/usr/share/wb-rules-modules/ $(DESTDIR)/etc/wb-rules-modules/
	install -Dm0755 wb-rules -t $(DESTDIR)$(PREFIX)/bin
	install -Dm0644 rules/rules.js -t $(DESTDIR)/etc/wb-rules
	install -Dm0644 wb-rules.wbconfigs $(DESTDIR)/etc/wb-configs.d/13wb-rules

	install -Dm0644 scripts/lib.js -t $(DESTDIR)$(PREFIX)/share/wb-rules-system/scripts
	install -Dm0644 rules/load_alarms.js -t $(DESTDIR)$(PREFIX)/share/wb-rules
	install -Dm0644 $(DEB_TARGET_ARCH).wbgo.so $(DESTDIR)$(PREFIX)/share/wb-rules/wbgo.so
	install -Dm0644 rules/alarms.conf -t $(DESTDIR)/etc/wb-rules
	install -Dm0644 rules/alarms.schema.json -t $(DESTDIR)$(PREFIX)/share/wb-mqtt-confed/schemas

deb:
	$(GO_ENV) dpkg-buildpackage -b -a$(DEB_TARGET_ARCH) -us -uc
