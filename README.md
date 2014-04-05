juju-nat
========
Deploying services into LXC and KVM containers provides nice isolation and
encapsulation. *_juju-nat_* sets up NAT routing to expose ports to service units
deployed inside containers.

Installing
==========
Install Go 1.2, http://golang.org.

Get a recent pull of juju-core & godeps, update all its package dependencies.

```
 $ go get -u launchpad.net/juju-core/...
 $ go get launchpad.net/godeps
 $ godeps -u $GOPATH/src/launchpad.net/juju-core/dependencies.tsv
```

Checkout, build and install.
```
 $ go get github.com/cmars/juju-nat
```

Example
=======

Given a service deployed into an LXC container:

```
 $ juju deploy owncloud --to lxc:0
 $ juju status owncloud
```

```
machines:
  "0":
    dns-name: 192.168.122.107
    containers:
      0/lxc/2:
        dns-name: 10.0.3.151
        instance-id: juju-machine-0-lxc-2
services:
  owncloud:
    charm: cs:precise/owncloud-12
    exposed: true
    units:
      owncloud/0:
        machine: 0/lxc/2
        open-ports:
        - 80/tcp
        public-address: 10.0.3.151
```

*_juju nat_* will expose port 80 on the containing machine (192.168.122.107), routed to
port 80 on the container where the service is deployed (10.0.3.151).
