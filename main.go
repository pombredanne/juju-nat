// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"log"
	"os"

	"launchpad.net/gnuflag"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/cmd/envcmd"
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
}

var doc = `TODO`

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

	st := conn.State

	/*
		machineMap := make(map[string]*state.Machine)
		machines, err := st.AllMachines()
		for _, m := range machines {
			machineMap[m.Id()] = m
		}
	*/

	services, err := st.AllServices()
	if err != nil {
		return err
	}
	for _, s := range services {
		//fmt.Println(s)
		units, err := s.AllUnits()
		if err != nil {
			return err
		}
		for _, u := range units {
			//fmt.Println(u)
			mid, err := u.AssignedMachineId()
			if err != nil {
				continue
			}
			/*
				m, ok := machineMap[mid]
				if !ok {
					log.Println("machine", mid, "not found")
				}
			*/
			parentMachine := state.ParentId(mid)
			if parentMachine != "" {
				log.Println(parentMachine, "parent of", mid)
				log.Println(u, "exposes", u.OpenedPorts())
			}
		}
	}

	return nil
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
