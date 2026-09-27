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
	case "harness", "engine":
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return
	default:
		usage()
	}
	var err error
	switch verb {
	case "tick", "start", "stop":
		err = refuseBesideEngine(verb)
	}
	switch {
	case err != nil:
	case verb == "tick":
		err = tick()
	case verb == "start":
		err = start()
	case verb == "stop":
		err = stop()
	case verb == "status":
		err = status()
	case verb == "init":
		err = initRepo(path)
	case verb == "remove":
		err = removeRepo(path)
	case verb == "harness":
		err = runHarness(os.Args[2:])
	case verb == "engine":
		err = runEngine(os.Args[2:])
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
  ghafk engine setup                       run the engine as its own system account (asks sudo); run again to refresh
  ghafk engine token                       replace the engine's GitHub token
  ghafk engine start | stop                turn the engine's timer on or off
  ghafk engine remove [--purge]            remove the engine service; --purge also deletes its account and home
  ghafk help                               show this help
`

func usage() {
	fmt.Fprint(os.Stderr, usageText)
	os.Exit(2)
}
