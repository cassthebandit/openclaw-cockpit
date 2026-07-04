//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package main

func disableHardTabOptimization() func() {
	return func() {}
}
