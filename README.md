SUMMARY
=======
Deploying services into LXC and KVM containers provides nice isolation and
encapsulation. juju nat-\* subcommands set up NAT routing to service units
deployed inside containers.

BUILD
=====
Install Go 1.2, http://golang.org.

Get a recent pull of juju-core & godeps, update all its package dependencies.

```
$ go get -u launchpad.net/juju-core/...
$ go get launchpad.net/godeps
$ godeps -u $GOPATH/src/launchpad.net/juju-core/dependencies.tsv
```

Checkout, build and install.

```
$ go install github.com/cmars/juju-nat/...
```

This will install binaries in $GOPATH/bin, adding the following juju subcommands.

USAGE
=====
Use --help on the subcommands for all the options. Here's a quick start:

juju nat-expose
---------------
Exposes ports on a unit running in a container, or a container machine address,
through to the containing host machine.

For example, with an apache service like:

```
machines:
  "0":
    dns-name: 69.16.230.117
    instance-id: 'manual:'
    series: precise
services:
  apache2:
    charm: local:precise/apache2-0
    exposed: true
    units:
      apache2/0:
        machine: 0/lxc/0
        open-ports:
        - 80/tcp
        - 443/tcp
        public-address: 10.0.3.254
```

'juju nat-expose 0/lxc/4' forwards inbound traffic on ports 80 and 443
from the containing machine '0' through to the apache2 running in the LXC
container. You can also use the service unit, 'juju nat-expose apache2/0'.

Outbound traffic is also routed from the container through the containing
machine.

You can remap ports with the -p (port map) option. This accepts a
comma-separated list of INTERNAL:EXTERNAL bindings, so that you can expose
multiple services that open the same ports.

juju nat-outbound
-----------------
In some cases, a service running in a container needs outbound routing through
the containing host machine. Use 'juju nat-outbound' on these units or
containers to set that up.

juju nat-clear
--------------
Wipes all the NAT routing rules created by the above commands, so you can start
over.  Actually, it clears all the iptables rules on the containing machines
that match, so you might want to make sure that's what you want before using it.

BUGS
====
Probably some. Networking is not my speciality, there are probably other ways
to pull this off, some may be better, let me know.  In the meantime, juju-nat
lets me use LXC containers with Juju in a very practical down-to-earth way.

Kind of silly to have to have one instance for every Juju service.  Natting
containers with the Juju manual provider is probably enough to run a whole lot
of services for personal use.  That's why I wrote this...
