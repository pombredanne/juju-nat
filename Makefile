SANDBOX=$(shell pwd)/Godeps/_workspace

CMDS=juju-nat-expose juju-nat-outbound juju-nat-reset
BINS=$(CMDS:%=$(SANDBOX)/bin/%)
GODEP=$(SANDBOX)/bin/godep

all: $(BINS)

$(SANDBOX)/bin/juju-nat-%: $(GODEP)
	$(GODEP) go build -o $@ github.com/cmars/juju-nat/cmd/$(shell basename $@)

$(GODEP):
	GOPATH=$(SANDBOX) go get github.com/tools/godep

clean:
	$(RM) -r $(SANDBOX)/bin $(SANDBOX)/pkg

distclean:
	$(RM) -r $(SANDBOX)

.PHONY: _godep distclean clean all
