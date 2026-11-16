package dispatch_test

import (
	"bytes"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal and returns its two ends: what is written to the terminal end
// can be read from the controlling end, as a terminal emulator reads it.
func openPTY() (control, terminal *os.File, err error) {
	control, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(control.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		_ = control.Close()
		return nil, nil, err
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		_ = control.Close()
		return nil, nil, err
	}
	name := make([]byte, 128)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		_ = control.Close()
		return nil, nil, errno
	}
	terminal, err = os.OpenFile(string(name[:bytes.IndexByte(name, 0)]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = control.Close()
		return nil, nil, err
	}
	return control, terminal, nil
}
