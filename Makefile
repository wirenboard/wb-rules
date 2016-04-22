.PHONY: all prepare clean

GOPATH := $(HOME)/go
PATH := $(GOPATH)/bin:$(PATH)

DEB_TARGET_ARCH ?= armel

ifeq ($(DEB_TARGET_ARCH),armel)
GO_ENV := GOARCH=arm GOARM=5 CC_FOR_TARGET=arm-linux-gnueabi-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),amd64)
GO_ENV := GOARCH=amd64 CC=x86_64-linux-gnu-gcc
endif
ifeq ($(DEB_TARGET_ARCH),i386)
GO_ENV := GOARCH=386 CC=i586-linux-gnu-gcc
endif

all: clean wb-rules

clean:
	rm -rf wb-rules

# We remove the box file after build because
# it may cause problems during development
# (changes in lib.js being ignored)

wb-rules: main.go wbrules/*.go
	$(GO_ENV) glide install
	$(GO_ENV) go build

install:
	mkdir -p $(DESTDIR)/usr/bin/ $(DESTDIR)/etc/init.d/ $(DESTDIR)/etc/wb-rules/ $(DESTDIR)/usr/share/wb-mqtt-confed/schemas $(DESTDIR)/etc/wb-configs.d $(DESTDIR)/usr/share/wb-rules-system/scripts/
	install -m 0755 wb-rules $(DESTDIR)/usr/bin/
	install -m 0755 initscripts/wb-rules $(DESTDIR)/etc/init.d/wb-rules
	install -m 0644 rules/rules.js $(DESTDIR)/etc/wb-rules/rules.js
	install -m 0644 wb-rules.wbconfigs $(DESTDIR)/etc/wb-configs.d/13wb-rules

	install -m 0644 scripts/lib.js $(DESTDIR)/usr/share/wb-rules-system/scripts/lib.js
	install -m 0644 rules/load_alarms.js $(DESTDIR)/etc/wb-rules/load_alarms.js
	install -m 0644 rules/alarms.conf $(DESTDIR)/etc/wb-rules/alarms.conf
	install -m 0644 rules/alarms.schema.json $(DESTDIR)/usr/share/wb-mqtt-confed/schemas/alarms.schema.json

deb:
	CC=arm-linux-gnueabi-gcc dpkg-buildpackage -b -aarmel -us -uc
