package service

import (
	"fmt"
	"os"
	"syscall"
)

// CollectorLock prevents two server processes from resetting the same counters.
// CLI processes never acquire it and never reset counters.
func CollectorLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another VPN collector is running: %w", err)
	}
	return f, nil // Closing the file releases the kernel lock, including on crash.
}
