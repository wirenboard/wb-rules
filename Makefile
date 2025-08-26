.PHONY: all clean

PREFIX = /usr
DEB_TARGET_ARCH ?= armhf
WBGO_LOCAL_PATH ?= .

ifeq ($(DEB_TARGET_ARCH),armhf)
GO_ENV := GOARCH=arm GOARM=6 CC_FOR_TARGET=arm-linux-gnueabihf-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),arm64)
GO_ENV := GOARCH=arm64 CC_FOR_TARGET=aarch64-linux-gnu-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),amd64)
GO_ENV := GOARCH=amd64
endif

GO_ENV := GO111MODULE=on $(GO_ENV)

GO ?= go
GO_FLAGS = -ldflags "-s -w -X main.version=`git describe --tags --always --dirty`"

all: clean wb-rules

clean:
	rm -rf wb-rules

amd64:
	$(MAKE) DEB_TARGET_ARCH=amd64

test:
	cp $(WBGO_LOCAL_PATH)/amd64.wbgo.so wbrules/wbgo.so
	$(GO) test -v -trimpath -ldflags="-s -w" -cover -gcflags=all='-N -l' -race ./wbrules

wb-rules: main.go wbrules/*.go
	$(GO_ENV) $(GO) build -trimpath $(GO_FLAGS)

install:
	mkdir -p $(DESTDIR)$(PREFIX)/share/wb-rules-modules/ $(DESTDIR)/etc/wb-rules-modules/
	install -Dm0755 wb-rules -t $(DESTDIR)$(PREFIX)/bin
	install -Dm0644 rules/rules.js -t $(DESTDIR)/etc/wb-rules
	install -Dm0644 wb-rules.wbconfigs $(DESTDIR)/etc/wb-configs.d/13wb-rules

	install -Dm0644 scripts/lib.js -t $(DESTDIR)$(PREFIX)/share/wb-rules-system/scripts
	install -Dm0644 rules/load_alarms.js -t $(DESTDIR)$(PREFIX)/share/wb-rules
	install -Dm0644 $(WBGO_LOCAL_PATH)/$(DEB_TARGET_ARCH).wbgo.so $(DESTDIR)$(PREFIX)/lib/wb-rules/wbgo.so
	install -Dm0644 rules/alarms.conf -t $(DESTDIR)/etc/wb-rules
	install -Dm0644 rules/alarms.schema.json -t $(DESTDIR)$(PREFIX)/share/wb-mqtt-confed/schemas

deb:
	$(GO_ENV) dpkg-buildpackage -b -a$(DEB_TARGET_ARCH) -us -uc
