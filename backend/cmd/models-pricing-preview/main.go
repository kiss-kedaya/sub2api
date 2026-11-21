// models-pricing-preview checks offline models.dev price coverage for an inventory.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("models-pricing-preview", flag.ContinueOnError)
	flags.SetOutput(stderr)
	snapshotPath := flags.String("snapshot", "", "local models.dev api.json snapshot")
	inventoryPath := flags.String("inventory", "", "local redacted pricing inventory JSON")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: models-pricing-preview -snapshot api.json -inventory inventory.json")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *snapshotPath == "" || *inventoryPath == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	snapshot, err := os.ReadFile(*snapshotPath)
	if err != nil {
		fmt.Fprintf(stderr, "read snapshot: %v\n", err)
		return 1
	}
	inventory, err := os.ReadFile(*inventoryPath)
	if err != nil {
		fmt.Fprintf(stderr, "read inventory: %v\n", err)
		return 1
	}
	report, err := service.PreviewModelsPricingCoverage(snapshot, inventory)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	return 0
}
