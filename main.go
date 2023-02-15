package main

import (
	"flag"
	"fmt"
	"os"
)

func init() {
	var err error

	_PID = os.Getpid()
	_hostname, err = os.Hostname()
	if err != nil {
		logDebug("Cannot find the host name, 'localhost' assumed")
		_hostname = "localhost"
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %v -c CONFIG_FILE\n\n", PROG_NAME)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "  -h\tShow this help page.\n")
	}

	cmdLineGet()
	readConfig()
}

func main() {
	RunServer()
}
