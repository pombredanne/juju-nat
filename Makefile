SANDBOX=$(shell pwd)/Godeps/_workspace

CMDS=juju-nat-expose juju-nat-outbound juju-nat-reset
BINS=$(CMDS:%=$(SANDBOX)/bin/%)
GODEP=$(SANDBOX)/bin/godep
MAINT_SIGKEY=0x879CF8AA8DDA301A

PREFIX=/usr/local

all: $(BINS)

install: all
	mkdir -p $(PREFIX)/bin
	install -m 0755 $(BINS) $(PREFIX)/bin

restore: $(GODEP)
	GOPATH=$(SANDBOX) $(GODEP) restore
	mkdir -p $(SANDBOX)/src/github.com/cmars/juju-nat
	git archive HEAD | (cd $(SANDBOX)/src/github.com/cmars/juju-nat; tar xvf -)

$(SANDBOX)/bin/juju-nat-%: $(GODEP)
	$(GODEP) go build -o $@ github.com/cmars/juju-nat/cmd/$(shell basename $@)

$(GODEP):
	GOPATH=$(SANDBOX) go get github.com/tools/godep

debbin: restore all
	debuild -us -uc -i -b

debsrc: debbin
	debuild -S -k$(MAINT_SIGKEY)

clean:
	$(RM) -r $(SANDBOX)/bin $(SANDBOX)/pkg

src-clean:
	$(RM) -r $(SANDBOX)

pkg-clean:
	$(RM) ../juju-nat_*.deb ../juju-nat_*.dsc ../juju-nat_*.changes ../juju-nat_*.build ../juju-nat_*.tar.gz ../juju-nat_*.upload

.PHONY: _godep all godepcheck restore debbin debsrc clean src-clean pkg-clean
