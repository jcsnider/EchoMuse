package server

import (
	"golang.org/x/sys/unix"
	"time"
)

func getUptime() (time.Duration, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0, err
	}
	return time.Second * time.Duration(info.Uptime), nil
}
