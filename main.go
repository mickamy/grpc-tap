package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mickamy/grpc-tap/tui"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("grpc-tap", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "grpc-tap — Watch gRPC traffic in real-time\n\nUsage:\n  grpc-tap [flags] <addr>\n\nFlags:\n")
		fs.PrintDefaults()
	}

	showVersion := fs.Bool("version", false, "show version and exit")

	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("grpc-tap %s\n", version)
		return
	}

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	m := tui.New(fs.Arg(0))
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
