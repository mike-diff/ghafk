//go:build darwin

package main

import "fmt"

const rootGroup = "wheel"

var errNoDarwinEngine = fmt.Errorf("ghafk engine is not available on macOS yet")

func installedEngine() (engineLayout, bool) { return engineLayout{}, false }

func engineSetup() error { return errNoDarwinEngine }

func engineStart() error { return errNoDarwinEngine }

func engineStop() error { return errNoDarwinEngine }

func engineRemove(bool) error { return errNoDarwinEngine }

func engineServiceState(engineLayout) string { return "" }

func reexecWithGroup(engineLayout) bool { return false }
