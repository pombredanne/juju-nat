// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"

	"launchpad.net/gnuflag"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/names"

	// Import the providers.
	_ "launchpad.net/juju-core/provider/all"

	natcmd "github.com/cmars/juju-nat/cmd"
)

type NatExposeCommand struct {
	natcmd.NatCommand
	dryRun      bool
	target      string
	portMapping string

	portMap  map[int]int
	forwards map[string][]*natcmd.Forward
}

var doc = `
'juju nat-expose' sets up NAT routing to expose ports to service units deployed inside
containers.
`

func (c *NatExposeCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "nat",
		Args:    "[args] <target>",
		Purpose: "Expose a service in a container to external ports on the host machine.",
		Doc:     doc,
	}
}

func (c *NatExposeCommand) SetFlags(f *gnuflag.FlagSet) {
	c.NatCommand.SetFlags(f)
	f.BoolVar(&c.dryRun, "dry-run", false, "show the NAT routing commands, but do not execute them")
	f.StringVar(&c.portMapping, "p", "", "port mapping(s), INTERNAL:EXTERAL[,INTERNAL:EXTERNAL,...]")
}

func (c *NatExposeCommand) Init(args []string) error {
	err := c.NatCommand.Init()
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
			portMap, err := natcmd.ParsePortMap(portMapping)
			if err != nil {
				return fmt.Errorf("invalid port mapping %q: %v", portMapping, err)
			}
			c.portMap[portMap.InternalPort] = portMap.ExternalPort
		}
	}
	return nil
}

func (c *NatExposeCommand) Run(ctx *cmd.Context) error {
	err := c.Connect(c.target)
	if err != nil {
		return err
	}
	c.forwards = make(map[string][]*natcmd.Forward)
	for _, uc := range c.ContainedUnits {
		if (names.IsUnit(c.target) && uc.Unit.Name() == c.target) ||
			(names.IsMachine(c.target) && (uc.HostMachine.Id() == c.target || uc.GatewayMachine.Id() == c.target)) {
			fwd, err := uc.NewForward()
			fwd.PortMap = c.portMap
			if err != nil {
				log.Println(err)
				continue
			}
			c.forwards[uc.GatewayMachine.Id()] = append(c.forwards[uc.GatewayMachine.Id()], fwd)
		}
	}
	if c.dryRun {
		c.printNatScripts()
	} else {
		c.execNatScripts()
	}
	return nil
}

func (c *NatExposeCommand) printNatScripts() {
	for machineId, fwds := range c.forwards {
		fmt.Printf("%s:\n", machineId)
		natcmd.WriteScriptStart(os.Stdout)
		for _, fwd := range fwds {
			fwd.Write(os.Stdout)
			fmt.Println()
		}
	}
}

func (c *NatExposeCommand) execNatScripts() {
	for machineId, fwds := range c.forwards {
		machine, ok := c.MachineMap[machineId]
		if !ok {
			log.Println("machine", machineId, "not found")
			continue
		}
		var natScript bytes.Buffer
		natcmd.WriteScriptStart(&natScript)
		for _, fwd := range fwds {
			fwd.Write(&natScript)
			fmt.Fprintln(&natScript)
		}
		err := c.ExecSsh(machine, natScript.String())
		if err != nil {
			log.Println("nat script failed on", machine.Id(), ":", err)
		}
	}
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
	c := &NatExposeCommand{}
	cmd.Main(c, ctx, os.Args[1:])
}
