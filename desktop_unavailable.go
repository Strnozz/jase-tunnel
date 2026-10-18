//go:build (darwin && !cgo) || (!darwin && !windows && !linux)

package main

import "log"

const desktopAvailable = false
const desktopUIName = ""

func prepareDesktopUI() error { return nil }

func (*application) runDesktopUI()         {}
func showDesktopUI()                       {}
func stopDesktopUI()                       {}
func confirmDesktopReplacement() bool      { return false }
func desktopStartupError(message string)   { log.Print(message) }
func notifyDesktop(id, title, body string) {}
