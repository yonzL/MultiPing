package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows ICMP helper API. Unlike raw sockets, IcmpSendEcho does not require
// administrator privileges and is safe to call from many goroutines at once.
var (
	iphlpapi              = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile    = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle   = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho      = iphlpapi.NewProc("IcmpSendEcho")
	icmpPayload           = []byte("MultiPing-0123456789abcdefghij")
	icmpReplyBufferSize   = uint32(1024)
	icmpPerAttemptTimeout = 1000 * time.Millisecond
)

const (
	ipSuccess        = 0
	pingAttemptCount = 3
)

type pinger struct {
	handle windows.Handle
}

func newPinger() (*pinger, error) {
	r, _, err := procIcmpCreateFile.Call()
	h := windows.Handle(r)
	if h == 0 || h == windows.InvalidHandle {
		return nil, fmt.Errorf("IcmpCreateFile 失败: %v", err)
	}
	return &pinger{handle: h}, nil
}

func (p *pinger) Close() {
	if p != nil && p.handle != 0 && p.handle != windows.InvalidHandle {
		_, _, _ = procIcmpCloseHandle.Call(uintptr(p.handle))
		p.handle = 0
	}
}

// pingOnce sends a single echo request. A return value of false means no reply
// was received within the timeout (or the request failed).
func (p *pinger) pingOnce(dest uint32) (time.Duration, bool) {
	if p.handle == 0 || len(icmpPayload) == 0 {
		return 0, false
	}

	reply := make([]byte, icmpReplyBufferSize)
	n, _, callErr := procIcmpSendEcho.Call(
		uintptr(p.handle),
		uintptr(dest),
		uintptr(unsafe.Pointer(&icmpPayload[0])),
		uintptr(uint16(len(icmpPayload))),
		0,
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(icmpReplyBufferSize),
		uintptr(icmpPerAttemptTimeout/time.Millisecond),
	)
	runtime.KeepAlive(icmpPayload)
	runtime.KeepAlive(reply)
	debugf("echo dest=%#x n=%d err=%v", dest, n, callErr)

	if n == 0 {
		return 0, false
	}

	// ICMP_ECHO_REPLY layout: Address(4), Status(4), RoundTripTime(4), ...
	status := binary.LittleEndian.Uint32(reply[4:8])
	debugf("echo dest=%#x status=%d", dest, status)
	if status != ipSuccess {
		return 0, false
	}
	rtt := binary.LittleEndian.Uint32(reply[8:12])
	return time.Duration(rtt) * time.Millisecond, true
}

// pingRound performs exactly three echo requests for one polling round.
// completed is false when the context was cancelled mid-round, in which case
// the caller should discard the partial result.
func (p *pinger) pingRound(ctx context.Context, dest uint32) (success int, avg time.Duration, completed bool) {
	var total time.Duration
	for i := 0; i < pingAttemptCount; i++ {
		if ctx.Err() != nil {
			return 0, 0, false
		}
		if rtt, ok := p.pingOnce(dest); ok {
			success++
			total += rtt
		}
		if i < pingAttemptCount-1 {
			select {
			case <-ctx.Done():
				return 0, 0, false
			case <-time.After(120 * time.Millisecond):
			}
		}
	}
	if success > 0 {
		avg = total / time.Duration(success)
	}
	return success, avg, true
}

// ipv4ToNetworkUint32 converts an IPv4 address to the representation expected
// by IcmpSendEcho: network byte order held in a uint32 (memory bytes a.b.c.d).
func ipv4ToNetworkUint32(ip [4]byte) uint32 {
	return uint32(ip[0]) | uint32(ip[1])<<8 | uint32(ip[2])<<16 | uint32(ip[3])<<24
}
