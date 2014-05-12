// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"bytes"
	"fmt"
	"log"
	"os"

	"launchpad.net/gnuflag"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/names"

	// Import the providers.
	_ "launchpad.net/juju-core/provider/all"

	natcmd "github.com/cmars/juju-nat/cmd"
)

type NatOutboundCommand struct {
	natcmd.NatCommand
	dryRun bool
	target string
}

var doc = `
'juju nat-outbound' sets up NAT routing to allow outbound traffic from a container
through its host machine.
`

func (c *NatOutboundCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "nat",
		Args:    "[args] <target>",
		Purpose: "Route a container's outbound traffic through the host machine.",
		Doc:     doc,
	}
}

func (c *NatOutboundCommand) SetFlags(f *gnuflag.FlagSet) {
	c.NatCommand.SetFlags(f)
	f.BoolVar(&c.dryRun, "dry-run", false, "show the NAT routing commands, but do not execute them")
}

func (c *NatOutboundCommand) Init(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no target name specified")
	}
	c.target = args[0]
	return nil
}

func (c *NatOutboundCommand) Run(ctx *cmd.Context) error {
	err := c.Connect(c.target)
	if err != nil {
		return err
	}
	var pending []natcmd.UnitContainment
	for _, uc := range c.ContainedUnits {
		if (names.IsUnit(c.target) && uc.Unit.Name() == c.target) ||
			(names.IsMachine(c.target) && (uc.HostMachine.Id() == c.target || uc.GatewayMachine.Id() == c.target)) {
			pending = append(pending, uc)
		}
	}
	if c.dryRun {
		c.printNatScripts(pending)
	} else {
		c.execNatScripts(pending)
	}
	return nil
}

func (c *NatOutboundCommand) printNatScripts(pending []natcmd.UnitContainment) {
	for _, uc := range pending {
		fmt.Printf("%s:\n", uc.GatewayMachine.Id)
		natcmd.WriteScriptStart(os.Stdout)
		natcmd.WriteScriptOutbound(os.Stdout, uc)
		fmt.Fprintln(os.Stdout)
	}
}

func (c *NatOutboundCommand) execNatScripts(pending []natcmd.UnitContainment) {
	for _, uc := range pending {
		var natScript bytes.Buffer
		natcmd.WriteScriptStart(&natScript)
		natcmd.WriteScriptOutbound(&natScript, uc)
		err := c.ExecSsh(uc.GatewayMachine, natScript.String())
		if err != nil {
			log.Println("nat script failed on", uc.GatewayMachine.Id(), ":", err)
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
	c := &NatOutboundCommand{}
	cmd.Main(c, ctx, os.Args[1:])
}
