package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	catalogPath := flag.String("catalog", "", "path to signal catalog JSON (required)")
	inputPath := flag.String("input", "", "path to JSONL log file (default: stdin)")
	summaryPath := flag.String("summary", "", "write summary JSON to this file (default: stderr)")
	flag.Parse()

	if *catalogPath == "" {
		fmt.Fprintln(os.Stderr, "error: -catalog is required")
		flag.Usage()
		os.Exit(2)
	}
	cat, err := LoadCatalog(*catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load catalog: %v\n", err)
		os.Exit(1)
	}

	in := os.Stdin
	if *inputPath != "" {
		f, err := os.Open(*inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: open input: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}

	sum, err := Process(cat, in, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: process: %v\n", err)
		os.Exit(1)
	}

	summaryOut := os.Stderr
	if *summaryPath != "" {
		f, err := os.Create(*summaryPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: create summary: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		summaryOut = f
	}
	enc := json.NewEncoder(summaryOut)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sum); err != nil {
		fmt.Fprintf(os.Stderr, "error: write summary: %v\n", err)
		os.Exit(1)
	}
}
