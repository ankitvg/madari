package main

import (
	"fmt"
	"os"
)

const version = "0.0.0-dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("madari - local MCP manager")
	fmt.Println()
	fmt.Println("Planned commands:")
	fmt.Println("  setup")
	fmt.Println("  add")
	fmt.Println("  remove")
	fmt.Println("  enable")
	fmt.Println("  disable")
	fmt.Println("  list")
	fmt.Println("  sync")
	fmt.Println("  doctor")
}
