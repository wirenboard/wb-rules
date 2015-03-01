GOM=gom
.PHONY: all clean

all: wb-rules
clean:

prepare:
	go get -u github.com/mattn/gom
	$(GOM) install

clean:
	rm -f wb-rules wbrules/*.rice-box.go

wb-rules: main.go wbrules/*.go
	(cd wbrules && rice embed-go)
	PATH=$(HOME)/progs/go/bin:$(PATH) GOARM=5 GOARCH=arm GOOS=linux \
	  CC_FOR_TARGET=arm-linux-gnueabi-gcc CGO_ENABLED=1 $(GOM) build
	rm -f wbrules/*.rice-box.go

install:
	mkdir -p $(DESTDIR)/usr/bin/ $(DESTDIR)/etc/init.d/ $(DESTDIR)/etc/wb-rules/
	install -m 0755 wb-rules $(DESTDIR)/usr/bin/
	install -m 0755 initscripts/wb-rules $(DESTDIR)/etc/init.d/wb-rules
	install -m 0655 rules/rules.js $(DESTDIR)/etc/wb-rules/rules.js
