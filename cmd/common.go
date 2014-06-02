package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"log"

	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/instance"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/names"
	"launchpad.net/juju-core/state"
	"launchpad.net/juju-core/utils/ssh"
)

var ErrNoContainer = fmt.Errorf("service unit not deployed in a container")

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

	PortMap map[int]int
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

func WriteScriptReset(w io.Writer) error {
	_, err := fmt.Fprintf(w, "/sbin/iptables -F\n/sbin/iptables -F -t nat\n")
	return err
}

func WriteScriptOutbound(w io.Writer, uc UnitContainment) error {
	f, err := uc.NewForward()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w,
		"/sbin/iptables -t nat -A POSTROUTING -s %s -o %s -j SNAT --to %s\n",
		f.InternalHostAddr, f.ExternalGatewayDevice, f.ExternalGatewayAddr)
	return err
}

func (f *Forward) Write(w io.Writer) error {
	for _, p := range f.InternalPorts {
		extPort, ok := f.PortMap[p.Number]
		if !ok {
			extPort = p.Number
		}

		_, err := fmt.Fprintf(w,
			"/sbin/iptables -t nat -A PREROUTING -p tcp -i %s -d %s --dport %d -j DNAT --to %s:%d\n",
			 f.ExternalGatewayDevice, f.ExternalGatewayAddr, extPort, f.InternalHostAddr, p.Number)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w,
			"/sbin/iptables -A FORWARD -p tcp -i %s -d %s --dport %d -j ACCEPT\n",
			 f.ExternalGatewayDevice, f.ExternalGatewayAddr, extPort)
		if err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w,
		"/sbin/iptables -t nat -A POSTROUTING -s %s -o %s -j SNAT --to %s\n",
		f.InternalHostAddr, f.ExternalGatewayDevice, f.ExternalGatewayAddr)
	return err
}

type NatCommand struct {
	cmd.EnvCommandBase

	Conn           *juju.Conn
	MachineMap     map[string]*state.Machine
	ContainedUnits []UnitContainment
}

func (c *NatCommand) Connect(target string) error {
	var err error
	c.Conn, err = juju.NewConnFromName(c.EnvName)
	if err != nil {
		return fmt.Errorf("Unable to connect to environment %q: %v", c.EnvName, err)
	}
	defer c.Conn.Close()

	if !names.IsMachine(target) && !names.IsUnit(target) {
		return fmt.Errorf("invalid target: %q", target)
	}

	c.MachineMap = make(map[string]*state.Machine)
	st := c.Conn.State

	machines, err := st.AllMachines()
	for _, m := range machines {
		c.MachineMap[m.Id()] = m
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
			uc, err := c.UnitContainment(u)
			if err == ErrNoContainer {
				continue
			} else if err != nil {
				log.Println(err)
				continue
			}
			c.ContainedUnits = append(c.ContainedUnits, *uc)
		}
	}
	return nil
}

func (c *NatCommand) ExecSsh(m *state.Machine, script string) error {
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

func (c *NatCommand) UnitContainment(u *state.Unit) (*UnitContainment, error) {
	machineId, err := u.AssignedMachineId()
	if err != nil {
		return nil, err
	}

	host, ok := c.MachineMap[machineId]
	if !ok {
		return nil, fmt.Errorf("machine not found: %q", machineId)
	}
	gatewayId := state.ParentId(machineId)
	if gatewayId == "" {
		// Ignore machines not in containers
		return nil, ErrNoContainer
	}
	gateway, ok := c.MachineMap[gatewayId]
	if !ok {
		return nil, fmt.Errorf("parent machine %q not found", gatewayId)
	}
	return &UnitContainment{Unit: u, GatewayMachine: gateway, HostMachine: host}, nil
}

func (u *UnitContainment) NewForward() (*Forward, error) {
	fwd := &Forward{
		UnitContainment:       *u,
		InternalPorts:         u.Unit.OpenedPorts(),
		ExternalGatewayDevice: "eth0",
		PortMap:               make(map[int]int),
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

func isLoopback(addr string) bool {
	return strings.HasPrefix(addr, "127.")
}

func MatchNetworks(host, gateway *state.Machine) (string, string, error) {
	var bestPrefix, bestHost, bestGw string
	for _, hostAddr := range host.Addresses() {
		if hostAddr.Type != instance.Ipv4Address || isLoopback(hostAddr.Value) {
			continue
		}
		for _, gwAddr := range gateway.Addresses() {
			if gwAddr.Type != instance.Ipv4Address || isLoopback(hostAddr.Value) {
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
