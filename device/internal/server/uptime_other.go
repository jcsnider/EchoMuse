//go:build !linux

package server

import (
	"errors"
	"time"
)

// Allows the hardware-independent server state tests to run on workstations.
func getUptime() (time.Duration, error) { return 0, errors.New("uptime requires Linux") }
