package main

import (
	"fmt"
	"os"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	commands := map[string]func([]string) error{
		"preflight": preflight,
		"write":     write,
		"verify":    verify,
	}
	if len(args) == 0 || commands[args[0]] == nil {
		fmt.Fprintln(os.Stderr, "usage: qualify-native preflight|write|verify [flags]")
		return 2
	}
	if err := commands[args[0]](args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
