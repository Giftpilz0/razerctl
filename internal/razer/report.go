// Package razer implements the Razer 90-byte feature-report protocol for the
// DeathAdder V2 Pro (1532:007C wired, 1532:007D wireless) and its Mouse Dock
// (1532:007E). Command layouts follow the OpenRazer kernel driver.
//
// Only runtime configuration commands are implemented. Nothing in this
// package can enter the bootloader or write firmware.
package razer

import (
	"errors"
	"fmt"
)

const (
	reportLen = 90
	maxArgs   = 80

	// Storage selector used by OpenRazer for this mouse ("VARSTORE").
	VarStore = 0x01
	NoStore  = 0x00

	LEDZero    = 0x00 // dock
	LEDBattery = 0x03
	LEDLogo    = 0x04
)

// Status is the first byte of a response.
type Status byte

const (
	StatusNew          Status = 0x00
	StatusBusy         Status = 0x01
	StatusOK           Status = 0x02
	StatusFailure      Status = 0x03
	StatusTimeout      Status = 0x04 // wireless: mouse asleep, off or out of range
	StatusNotSupported Status = 0x05
)

func (s Status) String() string {
	switch s {
	case StatusNew:
		return "new"
	case StatusBusy:
		return "busy"
	case StatusOK:
		return "ok"
	case StatusFailure:
		return "failure"
	case StatusTimeout:
		return "timeout"
	case StatusNotSupported:
		return "not supported"
	}
	return fmt.Sprintf("0x%02x", byte(s))
}

var (
	// ErrUnreachable means the receiver answered but the mouse did not
	// (asleep, switched off or out of range).
	ErrUnreachable  = errors.New("mouse not reachable (asleep or off)")
	ErrNotSupported = errors.New("command not supported by device")
)

// StatusError reports a non-OK status.
type StatusError struct {
	Class, Cmd byte
	Status     Status
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("command %02x:%02x failed: %s", e.Class, e.Cmd, e.Status)
}

func (e *StatusError) Is(target error) bool {
	switch target {
	case ErrUnreachable:
		return e.Status == StatusTimeout
	case ErrNotSupported:
		return e.Status == StatusNotSupported
	}
	return false
}

// Request is one command.
type Request struct {
	TID   byte // transaction id
	Class byte
	Cmd   byte
	Size  byte // declared argument length
	Args  []byte
}

// Response is a parsed reply.
type Response struct {
	Status Status
	TID    byte
	Size   byte
	Class  byte
	Cmd    byte
	Args   [maxArgs]byte
}

// Encode builds the 90-byte report:
// status, tid, remaining(2), protocol, size, class, cmd, args[80], crc, reserved.
func (r Request) Encode() ([reportLen]byte, error) {
	var b [reportLen]byte
	if len(r.Args) > maxArgs || int(r.Size) > maxArgs {
		return b, fmt.Errorf("too many arguments")
	}
	b[1] = r.TID
	b[5] = r.Size
	b[6] = r.Class
	b[7] = r.Cmd
	copy(b[8:], r.Args)
	b[88] = crc(b[:])
	return b, nil
}

// crc XORs bytes 2..87.
func crc(b []byte) byte {
	var c byte
	for _, v := range b[2:88] {
		c ^= v
	}
	return c
}

func decode(b []byte) (Response, bool) {
	var r Response
	if len(b) != reportLen || crc(b) != b[88] {
		return r, false
	}
	r.Status = Status(b[0])
	r.TID = b[1]
	r.Size = b[5]
	r.Class = b[6]
	r.Cmd = b[7]
	copy(r.Args[:], b[8:88])
	return r, true
}
