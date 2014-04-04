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

	// Import the providers.
	_ "launchpad.net/juju-core/provider/all"
)

type NatCommand struct {
	envcmd.EnvCommandBase
	out     cmd.Output
	service string
	port    int
	auto    bool

	machineMap map[string]*state.Machine
}

var doc = `TODO`

type Forward struct {
	ExternalGatewayAddr   string
	InternalGatewayAddr   string
	InternalHostAddr      string
	InternalPorts         []instance.Port
	ExternalGatewayDevice string
}

func (f *Forward) validate() error {
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

func (f Forward) Write(w io.Writer) error {
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

	var natScript bytes.Buffer
	WriteScriptStart(&natScript)
	c.machineMap = make(map[string]*state.Machine)
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
			fwd.Write(&natScript)
		}
	}
	fmt.Println(natScript.String())
	return nil
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

	fwd := &Forward{InternalPorts: u.OpenedPorts(), ExternalGatewayDevice: "eth0"}
	//gatewayAddrs := gateway.Addresses()

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
			prefix := greatestCommonPrefix(hostAddr.Value, gwAddr.Value)
			if len(prefix) > len(bestPrefix) {
				bestPrefix = prefix
				bestHost = hostAddr.Value
				bestGw = gwAddr.Value
			}
		}
	}
	if bestPrefix != "" && strings.Contains(bestPrefix, ".") {
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
