//go:build !darwin && !linux && !windows

package main

func totalMemory() uint64 { return 0 }
