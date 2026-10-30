//go:build !windows && !linux && !darwin

package main

func consoleWidth() int { return 0 }

func enableTerminalEscapes() {}
