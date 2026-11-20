package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows ICMP helper API. Unlike raw sockets, IcmpSendEcho2 does not require
// administrator privileges. The event-based form keeps requests asynchronous:
// a bounded worker pool waits on I/O events instead of parking one OS thread
// per host.
var (
	iphlpapi              = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile    = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle   = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho2     = iphlpapi.NewProc("IcmpSendEcho2")
	icmpPayload           = []byte("MultiPing-0123456789abcdefghij")
	icmpPerAttemptTimeout = 1000 * time.Millisecond
)

const (
	ipSuccess           = 0
	pingAttemptCount    = 3
	icmpReplyBufferSize = 1024

	// 16 workers x 64 slots = 1024 requests in flight, enough for 999 hosts.
	asyncWorkers = 16
	asyncSlots   = 64
	waitTimeout  = uint32(windows.WAIT_TIMEOUT)
)

type echoResult struct {
	latency time.Duration
	ok      bool
}

type echoRequest struct {
	dest uint32
	done chan echoResult
}

type echoSlot struct {
	event windows.Handle
	req   *echoRequest
	reply [icmpReplyBufferSize]byte
}

type pinger struct {
	handle    windows.Handle
	jobs      chan *echoRequest
	stop      chan struct{}
	events    []windows.Handle
	eventMu   sync.Mutex
	wg        sync.WaitGroup
	closed    atomic.Bool
	closeOnce sync.Once
}

func newPinger() (*pinger, error) {
	r, _, err := procIcmpCreateFile.Call()
	h := windows.Handle(r)
	if h == 0 || h == windows.InvalidHandle {
		return nil, fmt.Errorf("IcmpCreateFile 失败: %v", err)
	}

	p := &pinger{
		handle: h,
		jobs:   make(chan *echoRequest, asyncWorkers*asyncSlots),
		stop:   make(chan struct{}),
	}
	for i := 0; i < asyncWorkers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p, nil
}

func (p *pinger) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		close(p.stop)
		p.wg.Wait()
		p.eventMu.Lock()
		for _, ev := range p.events {
			_ = windows.CloseHandle(ev)
		}
		p.events = nil
		p.eventMu.Unlock()
		if p.handle != 0 && p.handle != windows.InvalidHandle {
			_, _, _ = procIcmpCloseHandle.Call(uintptr(p.handle))
			p.handle = 0
		}
	})
}

// ping starts one asynchronous echo request and waits for its completion.
// A cancelled context abandons the wait; the worker still completes the slot
// and discards the late result.
func (p *pinger) ping(ctx context.Context, dest uint32) (time.Duration, bool) {
	if p == nil || p.closed.Load() || p.handle == 0 {
		return 0, false
	}
	req := &echoRequest{dest: dest, done: make(chan echoResult, 1)}
	select {
	case p.jobs <- req:
	case <-ctx.Done():
		return 0, false
	case <-p.stop:
		return 0, false
	}
	select {
	case r := <-req.done:
		return r.latency, r.ok
	case <-ctx.Done():
		return 0, false
	case <-p.stop:
		return 0, false
	}
}

// pingOnce is kept for the existing round tests.
func (p *pinger) pingOnce(dest uint32) (time.Duration, bool) {
	return p.ping(context.Background(), dest)
}

func (p *pinger) worker() {
	defer p.wg.Done()
	slots := make([]*echoSlot, asyncSlots)
	for i := range slots {
		slots[i] = &echoSlot{}
	}
	active := 0
	handles := make([]windows.Handle, 0, len(slots))

	for {
		for active < len(slots) {
			select {
			case req := <-p.jobs:
				if p.closed.Load() {
					replyResult(req, 0, false)
					continue
				}
				slot := slots[active]
				if p.issue(slot, req) {
					active++
				}
			default:
				goto wait
			}
		}

	wait:
		if active == 0 {
			select {
			case req := <-p.jobs:
				if p.closed.Load() {
					replyResult(req, 0, false)
					continue
				}
				slot := slots[0]
				if p.issue(slot, req) {
					active = 1
				}
			case <-p.stop:
				return
			}
			continue
		}

		handles = handles[:0]
		for i := 0; i < active; i++ {
			handles = append(handles, slots[i].event)
		}
		r, err := windows.WaitForMultipleObjects(handles, false, 25)
		if err != nil {
			continue
		}
		if r == waitTimeout {
			continue
		}
		idx := int(r - windows.WAIT_OBJECT_0)
		if idx < 0 || idx >= active {
			continue
		}
		if slots[idx].req == nil {
			// A stale signal from an earlier immediate completion.
			_ = windows.ResetEvent(slots[idx].event)
			continue
		}
		p.finish(slots[idx])
		active--
		if idx != active {
			slots[idx], slots[active] = slots[active], slots[idx]
		}
	}
}

// issue sends one request. It returns true when the slot is now waiting for
// an event, or false when the result was already delivered synchronously.
func (p *pinger) issue(slot *echoSlot, req *echoRequest) bool {
	slot.req = req
	if slot.event == 0 {
		ev, eventErr := windows.CreateEvent(nil, 0, 0, nil)
		if eventErr != nil {
			debugf("CreateEvent: %v", eventErr)
			replyResult(req, 0, false)
			slot.req = nil
			return false
		}
		slot.event = ev
		p.eventMu.Lock()
		p.events = append(p.events, ev)
		p.eventMu.Unlock()
	}
	_ = windows.ResetEvent(slot.event)
	for i := range slot.reply {
		slot.reply[i] = 0xFF
	}
	n, _, callErr := procIcmpSendEcho2.Call(
		uintptr(p.handle),
		uintptr(slot.event),
		0, // no APC routine: completion signal goes to the event
		0, // no APC context
		uintptr(req.dest),
		uintptr(unsafe.Pointer(&icmpPayload[0])),
		uintptr(uint16(len(icmpPayload))),
		0,
		uintptr(unsafe.Pointer(&slot.reply[0])),
		uintptr(icmpReplyBufferSize),
		uintptr(icmpPerAttemptTimeout/time.Millisecond),
	)
	runtime.KeepAlive(icmpPayload)
	runtime.KeepAlive(slot.reply)

	if n != 0 {
		latency, ok := parseEchoReply(slot.reply[:])
		replyResult(req, latency, ok)
		slot.req = nil
		return false
	}
	if callErr == windows.ERROR_IO_PENDING {
		return true
	}
	debugf("echo async dest=%#x err=%v", req.dest, callErr)
	replyResult(req, 0, false)
	slot.req = nil
	return false
}

func (p *pinger) finish(slot *echoSlot) {
	req := slot.req
	if req == nil {
		return
	}
	latency, ok := parseEchoReply(slot.reply[:])
	debugf("echo done dest=%#x ok=%v latency=%v status=%d raw=%x", req.dest, ok, latency, binary.LittleEndian.Uint32(slot.reply[4:8]), slot.reply[:12])
	replyResult(req, latency, ok)
	slot.req = nil
}

func replyResult(req *echoRequest, latency time.Duration, ok bool) {
	select {
	case req.done <- echoResult{latency: latency, ok: ok}:
	default:
	}
}

func parseEchoReply(reply []byte) (time.Duration, bool) {
	if len(reply) < 12 {
		return 0, false
	}
	// ICMP_ECHO_REPLY layout: Address(4), Status(4), RoundTripTime(4), ...
	// A sentinel-filled buffer remains 0xFFFFFFFF on timeout, so its status
	// cannot be mistaken for IP_SUCCESS.
	status := binary.LittleEndian.Uint32(reply[4:8])
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
		if rtt, ok := p.ping(ctx, dest); ok {
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
