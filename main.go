package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	catalogPath := flag.String("catalog", "", "path to signal catalog JSON (required)")
	inputPath := flag.String("input", "", "path to JSONL messages (default: stdin)")
	outputPath := flag.String("output", "", "path to write JSONL results (default: stdout)")
	flag.Parse()
	if *catalogPath == "" {
		fmt.Fprintln(os.Stderr, "error: -catalog is required")
		flag.Usage()
		os.Exit(2)
	}
	cat, err := LoadCatalog(*catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	in := os.Stdin
	if *inputPath != "" {
		f, err := os.Open(*inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	out := os.Stdout
	if *outputPath != "" {
		f, err := os.Create(*outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}
	if _, err := Process(cat, in, out); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
