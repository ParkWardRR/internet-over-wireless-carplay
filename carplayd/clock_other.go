//go:build !linux

package main

import "time"

var start = time.Now()

func boottime() float64 { return time.Since(start).Seconds() }
