SANDBOX=$(shell pwd)/Godeps/_workspace

CMDS=juju-nat-expose juju-nat-outbound juju-nat-reset
BINS=$(CMDS:%=$(SANDBOX)/bin/%)
GODEP=$(SANDBOX)/bin/godep
MAINT_SIGKEY=0x879CF8AA8DDA301A

all: $(BINS)

restore:
	GOPATH=$(SANDBOX) $(GODEP) restore

$(SANDBOX)/bin/juju-nat-%: $(GODEP)
	$(GODEP) go build -o $@ github.com/cmars/juju-nat/cmd/$(shell basename $@)

$(GODEP):
	GOPATH=$(SANDBOX) go get github.com/tools/godep

debbin: all
	mkdir -p $(SANDBOX)/src/github.com/cmars/juju-nat
	git archive HEAD | (cd $(SANDBOX)/src/github.com/cmars/juju-nat; tar xvf -)
	debuild -us -uc -i -b

debsrc: debbin
	debuild -S -k$(MAINT_SIGKEY)

clean:
	$(RM) -r $(SANDBOX)/bin $(SANDBOX)/pkg

src-clean:
	$(RM) -r $(SANDBOX)

pkg-clean:
	$(RM) ../juju-nat_*.deb ../juju-nat_*.dsc ../juju-nat_*.changes ../juju-nat_*.build ../juju-nat_*.tar.gz 

.PHONY: _godep all debbin debsrc clean src-clean
