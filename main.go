package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	verb, path := os.Args[1], "."
	switch verb {
	case "tick", "start", "stop", "status":
		if len(os.Args) != 2 {
			usage()
		}
	case "init", "remove":
		if len(os.Args) > 3 {
			usage()
		}
		if len(os.Args) == 3 {
			path = os.Args[2]
		}
	case "harness":
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return
	default:
		usage()
	}
	var err error
	switch verb {
	case "tick":
		err = tick()
	case "start":
		err = start()
	case "stop":
		err = stop()
	case "status":
		err = status()
	case "init":
		err = initRepo(path)
	case "remove":
		err = removeRepo(path)
	case "harness":
		err = runHarness(os.Args[2:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ghafk:", err)
		os.Exit(1)
	}
}

const usageText = `ghafk works GitHub issues labeled "agent" with a coding agent while you are away.

Usage:
  ghafk init [path]                        register a repository and write .ghafk/WORKFLOW.md if it has none
  ghafk remove [path]                      unregister a repository; leaves its files, labels and pull requests
  ghafk start                              install and start the timer: systemd on Linux, launchd on macOS
  ghafk stop                               stop the timer; a running tick finishes first
  ghafk status                             show the timer, recent log, engine account and repository readiness
  ghafk tick                               run one pass over every registered repository now
  ghafk harness list                       list harness profiles and whether each is installed
  ghafk harness default <harness> <model>  set the machine default worker
  ghafk harness test <harness> [model]     check a harness in a scratch repository
  ghafk help                               show this help
`

func usage() {
	fmt.Fprint(os.Stderr, usageText)
	os.Exit(2)
}
