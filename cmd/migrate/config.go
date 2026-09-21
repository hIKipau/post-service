package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"post-service/internal/migrator"
)

type options struct {
	command string
	timeout time.Duration
}

// parseOptions requires an explicit command and a positive overall migration timeout.
func parseOptions(args []string, output io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "overall migration timeout, including lock acquisition")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: migrate [-timeout 5m] <up|down|status|version>")
		fmt.Fprintln(output, "Uses DATABASE_URL from the environment or optional .env file.")
		fmt.Fprintln(output, "WARNING: down rolls back one migration and can permanently delete data.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 1 {
		return options{}, errors.New("expected one command: up, down, status or version (flags must precede the command)")
	}
	if opts.timeout <= 0 {
		return options{}, errors.New("timeout must be positive")
	}
	opts.command = flags.Arg(0)
	if err := migrator.ValidateCommand(opts.command); err != nil {
		return options{}, err
	}
	return opts, nil
}
