package razer

import (
	"fmt"
	"sync"
	"time"

	"github.com/mschmidt/razerctl/internal/hidraw"
)

// featureDevice is what Conn needs from a hidraw node (faked in tests).
type featureDevice interface {
	SetFeature([]byte) error
	GetFeature([]byte) error
	Close() error
}

// Conn is an open Razer feature-report interface.
type Conn struct {
	Node hidraw.Node
	dev  featureDevice

	// wait is how long the device needs before its reply can be read.
	// OpenRazer uses ~60 ms for this receiver and ~31 ms for the dock.
	wait time.Duration

	mu sync.Mutex
}

// Dial opens node. It does not send anything.
func Dial(node hidraw.Node, wait time.Duration) (*Conn, error) {
	dev, err := hidraw.Open(node.Path)
	if err != nil {
		return nil, err
	}
	return &Conn{Node: node, dev: dev, wait: wait}, nil
}

func (c *Conn) Close() error { return c.dev.Close() }

// Do sends req and returns the matching response. A busy device is retried;
// any status other than OK is returned as a *StatusError.
func (c *Conn) Do(req Request) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lock, err := hidraw.AcquireLock()
	if err != nil {
		return Response{}, fmt.Errorf("lock: %w", err)
	}
	defer lock.Release()

	enc, err := req.Encode()
	if err != nil {
		return Response{}, err
	}
	// hidraw expects the report ID (0) in front of the 90-byte report.
	out := make([]byte, reportLen+1)
	copy(out[1:], enc[:])

	var last Response
	// A write is sent exactly once; only reads are repeated.
	attempts := 1
	if req.Cmd >= 0x80 {
		attempts = 3
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := c.dev.SetFeature(out); err != nil {
			return Response{}, err
		}
		time.Sleep(c.wait)

		deadline := time.Now().Add(time.Second)
	poll:
		for time.Now().Before(deadline) {
			in := make([]byte, reportLen+1)
			if err := c.dev.GetFeature(in); err != nil {
				return Response{}, err
			}
			resp, ok := decode(in[1:])
			if ok && resp.Class == req.Class && resp.Cmd == req.Cmd {
				last = resp
				switch resp.Status {
				case StatusBusy, StatusNew:
				case StatusOK:
					return resp, nil
				case StatusFailure:
					// The receiver sporadically reports failure. Reads
					// (cmd 0x80 and up) are sent again; a write is not,
					// since it may have applied anyway: the caller reads
					// the state back instead of writing blindly.
					if attempt < attempts-1 {
						time.Sleep(50 * time.Millisecond)
						break poll
					}
					fallthrough
				default:
					return resp, &StatusError{req.Class, req.Cmd, resp.Status}
				}
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	if last.Status == StatusBusy {
		return last, &StatusError{req.Class, req.Cmd, StatusBusy}
	}
	return last, fmt.Errorf("command %02x:%02x: no response", req.Class, req.Cmd)
}

// FirmwareVersion works on every Razer interface that speaks the protocol,
// which makes it the probe used to pick the right hidraw node.
func (c *Conn) FirmwareVersion(tid byte) (string, error) {
	r, err := c.Do(Request{TID: tid, Class: 0x00, Cmd: 0x81, Size: 0x02})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("v%d.%02d", r.Args[0], r.Args[1]), nil
}

// Serial reads the device serial number.
func (c *Conn) Serial(tid byte) (string, error) {
	r, err := c.Do(Request{TID: tid, Class: 0x00, Cmd: 0x82, Size: 0x16})
	if err != nil {
		return "", err
	}
	n := 0
	for n < 0x16 && r.Args[n] != 0 {
		n++
	}
	return string(r.Args[:n]), nil
}
