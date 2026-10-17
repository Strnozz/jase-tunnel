//go:build !darwin || !cgo

package main

import "log"

const desktopAvailable = false

func (*application) runDesktopUI()       {}
func showDesktopUI()                     {}
func stopDesktopUI()                     {}
func confirmDesktopReplacement() bool    { return false }
func desktopStartupError(message string) { log.Print(message) }
