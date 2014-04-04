juju-nat
========
juju-nat sets up NAT routing to expose ports to service units deployed inside
containers.

Example
=======

Given a service deployed to an LXC container:

 $ juju deploy wordpress --to lxc:0
 $ juju status wordpress

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

'juju nat' will expose port 80 on the containing machine (192.168.122.107), routed to
port 80 on the container where the service is deployed (10.0.3.151).

Limitations
===========

Not yet supported or tested
---------------------------

NAT routing information not known to Juju, not available in 'juju status'.

Remapping internal ports to different external ports.

Checking for collisions. Last one picked, wins right now (I think). Not
terribly useful...

NAT routing for containers inside containers.

Filtering to specific machines or services.

Assumptions
-----------

Public gateway host interface always assumed to be eth0. If it's different for
some reason, iptables will not work properly.

Contact
=======
Casey Marshall <casey.marshall@canonical.com>
cmars on freenode (#juju, #juju-dev)
