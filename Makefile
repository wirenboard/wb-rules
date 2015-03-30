GOM=gom
.PHONY: all clean

all: clean wb-rules
clean:

prepare:
	go get -u github.com/mattn/gom
	go get -u github.com/GeertJohan/go.rice
	go get -u github.com/GeertJohan/go.rice/rice
	PATH=$(HOME)/progs/go/bin:$(PATH) GOARM=5 GOARCH=arm GOOS=linux \
	  CC_FOR_TARGET=arm-linux-gnueabi-gcc CGO_ENABLED=1 $(GOM) install

clean:
	rm -f wb-rules wbrules/*.rice-box.go _vendor/

# We remove the box file after build because
# it may cause problems during development
# (changes in lib.js being ignored)

wb-rules: main.go wbrules/*.go
	(cd wbrules && $(HOME)/go/bin/rice embed-go)
	PATH=$(HOME)/progs/go/bin:$(PATH) GOARM=5 GOARCH=arm GOOS=linux \
	  CC_FOR_TARGET=arm-linux-gnueabi-gcc CGO_ENABLED=1 $(GOM) build
	rm -f wbrules/*.rice-box.go

install:
	mkdir -p $(DESTDIR)/usr/bin/ $(DESTDIR)/etc/init.d/ $(DESTDIR)/etc/wb-rules/
	install -m 0755 wb-rules $(DESTDIR)/usr/bin/
	install -m 0755 initscripts/wb-rules $(DESTDIR)/etc/init.d/wb-rules
	install -m 0655 rules/rules.js $(DESTDIR)/etc/wb-rules/rules.js
