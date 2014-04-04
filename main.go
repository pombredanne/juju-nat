// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"launchpad.net/gnuflag"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/cmd/envcmd"
	"launchpad.net/juju-core/instance"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/state"
	"launchpad.net/juju-core/utils/ssh"

	// Import the providers.
	_ "launchpad.net/juju-core/provider/all"
)

type NatCommand struct {
	envcmd.EnvCommandBase
	dryRun bool

	machineMap map[string]*state.Machine
	forwards   map[string][]*Forward
}

var doc = `
juju-nat sets up NAT routing to expose ports to service units deployed inside
containers.

Example:

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
`

type Forward struct {
	GatewayMachine        *state.Machine
	ExternalGatewayAddr   string
	InternalGatewayAddr   string
	InternalHostAddr      string
	InternalPorts         []instance.Port
	ExternalGatewayDevice string
}

func (f *Forward) validate() error {
	if f.GatewayMachine == nil {
		return fmt.Errorf("external gateway machine not defined")
	}
	if f.ExternalGatewayAddr == "" {
		return fmt.Errorf("external gateway address not found")
	}
	if f.ExternalGatewayDevice == "" {
		return fmt.Errorf("external gateway device not found")
	}
	if f.InternalHostAddr == "" {
		return fmt.Errorf("internal host address not found")
	}
	if len(f.InternalPorts) == 0 {
		return fmt.Errorf("no ports to forward")
	}
	return nil
}

func WriteScriptStart(w io.Writer) error {
	_, err := fmt.Fprintf(w, `#!/bin/sh
/sbin/iptables -F
/sbin/iptables -F -t nat
`)
	return err
}

func (f *Forward) Write(w io.Writer) error {
	for _, p := range f.InternalPorts {
		_, err := fmt.Fprintf(w,
			"/sbin/iptables -t nat -A PREROUTING -p tcp -i eth0 -d %s --dport %d -j DNAT --to %s:%d\n",
			f.ExternalGatewayAddr, p.Number, f.InternalHostAddr, p.Number)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w,
			"/sbin/iptables -A FORWARD -p tcp -i eth0 -d %s --dport %d -j ACCEPT\n",
			f.ExternalGatewayAddr, p.Number)
		if err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w,
		"/sbin/iptables -t nat -A POSTROUTING -s %s -o eth0 -j SNAT --to %s\n",
		f.InternalHostAddr, f.ExternalGatewayAddr)
	return err
}

func (c *NatCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "nat",
		Args:    "[args and stuff]",
		Purpose: "Expose a service in a container to external ports on the host machine.",
		Doc:     doc,
	}
}

func (c *NatCommand) SetFlags(f *gnuflag.FlagSet) {
	c.EnvCommandBase.SetFlags(f)
	f.BoolVar(&c.dryRun, "dry-run", false, "show the NAT routing commands, but do not execute them")
}

func (c *NatCommand) Init(args []string) error {
	return c.EnvCommandBase.Init()
}

func (c *NatCommand) Run(ctx *cmd.Context) error {
	conn, err := juju.NewConnFromName(c.EnvName)
	if err != nil {
		return fmt.Errorf("Unable to connect to environment %q: %v", c.EnvName, err)
	}
	defer conn.Close()

	c.machineMap = make(map[string]*state.Machine)
	c.forwards = make(map[string][]*Forward)
	st := conn.State

	machines, err := st.AllMachines()
	for _, m := range machines {
		c.machineMap[m.Id()] = m
	}

	services, err := st.AllServices()
	if err != nil {
		return err
	}
	for _, s := range services {
		units, err := s.AllUnits()
		if err != nil {
			return err
		}
		for _, u := range units {
			fwd, err := c.newForward(u)
			if err != nil {
				log.Println(err)
				continue
			}
			c.forwards[fwd.GatewayMachine.Id()] = append(c.forwards[fwd.GatewayMachine.Id()], fwd)
		}
	}
	if c.dryRun {
		c.printNatScripts()
	} else {
		c.execNatScripts()
	}
	return nil
}

func (c *NatCommand) printNatScripts() {
	for machineId, fwds := range c.forwards {
		fmt.Printf("%s:\n", machineId)
		WriteScriptStart(os.Stdout)
		for _, fwd := range fwds {
			fwd.Write(os.Stdout)
			fmt.Println()
		}
	}
}

func (c *NatCommand) execNatScripts() {
	for machineId, fwds := range c.forwards {
		machine, ok := c.machineMap[machineId]
		if !ok {
			log.Println("machine", machineId, "not found")
			continue
		}
		var natScript bytes.Buffer
		WriteScriptStart(&natScript)
		for _, fwd := range fwds {
			fwd.Write(&natScript)
			fmt.Fprintln(&natScript)
		}
		err := c.execSsh(machine, natScript.String())
		if err != nil {
			log.Println("nat script failed on", machine.Id(), ":", err)
		}
	}
}

func (c *NatCommand) execSsh(m *state.Machine, script string) error {
	host := instance.SelectPublicAddress(m.Addresses())
	if host == "" {
		return fmt.Errorf("could not resolve machine's public address")
	}
	log.Println("Configuring NAT routing on machine ", m.Id())
	var options ssh.Options
	cmd := ssh.Command("ubuntu@"+host, []string{"sh -c 'NATCMD=$(mktemp); cat >${NATCMD}; sudo sh -x ${NATCMD}'"}, &options)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *NatCommand) newForward(u *state.Unit) (*Forward, error) {
	machineId, err := u.AssignedMachineId()
	if err != nil {
		return nil, err
	}

	host, ok := c.machineMap[machineId]
	if !ok {
		log.Println("machine", machineId, "not found")
		return nil, err
	}
	gatewayId := state.ParentId(machineId)
	if gatewayId == "" {
		// Ignore machines not in containers
		return nil, err
	}
	gateway, ok := c.machineMap[gatewayId]
	if !ok {
		log.Println("parent machine", gatewayId, "not found")
		return nil, err
	}

	fwd := &Forward{
		GatewayMachine:        gateway,
		InternalPorts:         u.OpenedPorts(),
		ExternalGatewayDevice: "eth0",
	}

	fwd.InternalHostAddr, fwd.InternalGatewayAddr, err = MatchNetworks(host, gateway)
	if err != nil {
		log.Println("failed to find network for NAT routing", machineId)
		return nil, err
	}

	if fwd.ExternalGatewayAddr = instance.SelectPublicAddress(gateway.Addresses()); fwd.ExternalGatewayAddr == "" {
		log.Println("failed to get internal address for", machineId, ": skipping")
		return nil, err
	}
	return fwd, fwd.validate()
}

func MatchNetworks(host, gateway *state.Machine) (string, string, error) {
	var bestPrefix, bestHost, bestGw string
	for _, hostAddr := range host.Addresses() {
		if hostAddr.Type != instance.Ipv4Address {
			continue
		} // for now...
		for _, gwAddr := range gateway.Addresses() {
			if gwAddr.Type != instance.Ipv4Address {
				continue
			}
			prefix := greatestCommonPrefix(hostAddr.Value, gwAddr.Value)
			if len(prefix) > len(bestPrefix) {
				bestPrefix = prefix
				bestHost = hostAddr.Value
				bestGw = gwAddr.Value
			}
		}
	}
	if bestHost != "" && bestGw != "" {
		return bestHost, bestGw, nil
	} else {
		return "", "", fmt.Errorf("failed to find common network for %s and %s", host.Id(), gateway.Id())
	}
}

func greatestCommonPrefix(s1, s2 string) string {
	var result []rune
	for i := 0; i < len(s1) && i < len(s2); i++ {
		if s1[i] != s2[i] {
			return string(result)
		}
		result = append(result, rune(s1[i]))
	}
	return string(result)
}

func main() {
	err := juju.InitJujuHome()
	if err != nil {
		panic(err)
	}
	ctx, err := cmd.DefaultContext()
	if err != nil {
		panic(err)
	}
	c := &NatCommand{}
	cmd.Main(c, ctx, os.Args[1:])
}
