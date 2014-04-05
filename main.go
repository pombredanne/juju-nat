// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"launchpad.net/gnuflag"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/cmd/envcmd"
	"launchpad.net/juju-core/instance"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/names"
	"launchpad.net/juju-core/state"
	"launchpad.net/juju-core/utils/ssh"

	// Import the providers.
	_ "launchpad.net/juju-core/provider/all"
)

type NatCommand struct {
	envcmd.EnvCommandBase
	dryRun      bool
	clear       bool
	target      string
	portMapping string

	portMap    map[int]int
	machineMap map[string]*state.Machine
	forwards   map[string][]*Forward
}

var doc = `
juju-nat sets up NAT routing to expose ports to service units deployed inside
containers.
`

type PortMap struct {
	InternalPort int
	ExternalPort int
}

func (p *PortMap) String() string {
	return fmt.Sprintf("%d:%d", p.InternalPort, p.ExternalPort)
}

func ParsePortMap(s string) (*PortMap, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid port mapping: %q", s)
	}
	intPort, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, err
	}
	extPort, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, err
	}
	return &PortMap{InternalPort: intPort, ExternalPort: extPort}, nil
}

type UnitContainment struct {
	Unit           *state.Unit
	GatewayMachine *state.Machine
	HostMachine    *state.Machine
}

type Forward struct {
	UnitContainment
	ExternalGatewayAddr   string
	InternalGatewayAddr   string
	InternalHostAddr      string
	InternalPorts         []instance.Port
	ExternalGatewayDevice string

	portMap map[int]int
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
	_, err := fmt.Fprintf(w, "#!/bin/sh\n")
	return err
}

func WriteScriptClear(w io.Writer) error {
	_, err := fmt.Fprintf(w, "/sbin/iptables -F\n/sbin/iptables -F -t nat\n")
	return err
}

func (f *Forward) Write(w io.Writer) error {
	for _, p := range f.InternalPorts {
		extPort, ok := f.portMap[p.Number]
		if !ok {
			extPort = p.Number
		}

		_, err := fmt.Fprintf(w,
			"/sbin/iptables -t nat -A PREROUTING -p tcp -i eth0 -d %s --dport %d -j DNAT --to %s:%d\n",
			f.ExternalGatewayAddr, extPort, f.InternalHostAddr, p.Number)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w,
			"/sbin/iptables -A FORWARD -p tcp -i eth0 -d %s --dport %d -j ACCEPT\n",
			f.ExternalGatewayAddr, extPort)
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
		Args:    "[args] <target>",
		Purpose: "Expose a service in a container to external ports on the host machine.",
		Doc:     doc,
	}
}

func (c *NatCommand) SetFlags(f *gnuflag.FlagSet) {
	c.EnvCommandBase.SetFlags(f)
	f.BoolVar(&c.dryRun, "dry-run", false, "show the NAT routing commands, but do not execute them")
	f.BoolVar(&c.clear, "clear", false, "clear all routing before setting up NAT")
	f.StringVar(&c.portMapping, "p", "", "port mapping(s), INTERNAL:EXTERAL[,INTERNAL:EXTERNAL,...]")
}

func (c *NatCommand) Init(args []string) error {
	err := c.EnvCommandBase.Init()
	if err != nil {
		return err
	}
	c.portMap = make(map[int]int)
	if len(args) == 0 {
		return fmt.Errorf("no target name specified")
	}
	c.target = args[0]

	if c.portMapping != "" {
		portMappings := strings.Split(c.portMapping, ",")
		for _, portMapping := range portMappings {
			portMap, err := ParsePortMap(portMapping)
			if err != nil {
				return fmt.Errorf("invalid port mapping %q: %v", portMapping, err)
			}
			c.portMap[portMap.InternalPort] = portMap.ExternalPort
		}
	}
	return nil
}

func (c *NatCommand) Run(ctx *cmd.Context) error {
	conn, err := juju.NewConnFromName(c.EnvName)
	if err != nil {
		return fmt.Errorf("Unable to connect to environment %q: %v", c.EnvName, err)
	}
	defer conn.Close()

	if !names.IsMachine(c.target) && !names.IsUnit(c.target) {
		return fmt.Errorf("invalid target: %q", c.target)
	}

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
			uc, err := c.unitContainment(u)
			if err == ErrNoContainer {
				continue
			} else if err != nil {
				log.Println(err)
				continue
			}

			if (names.IsUnit(c.target) && u.Name() == c.target) ||
				(names.IsMachine(c.target) && (uc.HostMachine.Id() == c.target || uc.GatewayMachine.Id() == c.target)) {
				fwd, err := uc.newForward()
				fwd.portMap = c.portMap
				if err != nil {
					log.Println(err)
					continue
				}
				c.forwards[uc.GatewayMachine.Id()] = append(c.forwards[uc.GatewayMachine.Id()], fwd)
			}
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
		if c.clear {
			WriteScriptClear(os.Stdout)
		}
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
		if c.clear {
			WriteScriptClear(&natScript)
		}
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

var ErrNoContainer = fmt.Errorf("service unit not deployed in a container")

func (c *NatCommand) unitContainment(u *state.Unit) (*UnitContainment, error) {
	machineId, err := u.AssignedMachineId()
	if err != nil {
		return nil, err
	}

	host, ok := c.machineMap[machineId]
	if !ok {
		return nil, fmt.Errorf("machine not found: %q", machineId)
	}
	gatewayId := state.ParentId(machineId)
	if gatewayId == "" {
		// Ignore machines not in containers
		return nil, ErrNoContainer
	}
	gateway, ok := c.machineMap[gatewayId]
	if !ok {
		return nil, fmt.Errorf("parent machine %q not found", gatewayId)
	}
	return &UnitContainment{Unit: u, GatewayMachine: gateway, HostMachine: host}, nil
}

func (u *UnitContainment) newForward() (*Forward, error) {
	fwd := &Forward{
		UnitContainment:       *u,
		InternalPorts:         u.Unit.OpenedPorts(),
		ExternalGatewayDevice: "eth0",
		portMap:               make(map[int]int),
	}

	var err error
	fwd.InternalHostAddr, fwd.InternalGatewayAddr, err = MatchNetworks(u.HostMachine, u.GatewayMachine)
	if err != nil {
		return nil, err
	}

	if fwd.ExternalGatewayAddr = instance.SelectPublicAddress(u.GatewayMachine.Addresses()); fwd.ExternalGatewayAddr == "" {
		return nil, fmt.Errorf("failed to get internal address: %q", u.GatewayMachine.Id())
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
