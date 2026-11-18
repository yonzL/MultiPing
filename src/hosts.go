package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const maxHosts = 400

type hostSpec struct {
	name string
	ip   string
	addr uint32
}

type Host struct {
	Name string
	IP   string
	Addr uint32

	mu       sync.Mutex
	success  int
	latency  time.Duration
	hasReply bool
	checked  time.Time
}

func (h *Host) reset() {
	h.mu.Lock()
	h.success = 0
	h.latency = 0
	h.hasReply = false
	h.checked = time.Time{}
	h.mu.Unlock()
}

func (h *Host) setResult(success int, latency time.Duration) {
	h.mu.Lock()
	h.success = success
	h.latency = latency
	h.hasReply = true
	h.checked = time.Now()
	h.mu.Unlock()
}

func (h *Host) snapshot() (success int, latency time.Duration, hasReply bool, checked time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.success, h.latency, h.hasReply, h.checked
}

// loadHostsFile reads a UTF-8 or GB18030/GBK CSV file with the columns
// hostName,hostIP. Invalid rows are skipped and reported in warnings; a
// malformed file as a whole returns an error.
func loadHostsFile(path string) (specs []hostSpec, warnings []string, skipped int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(data) == 0 {
		return nil, nil, 0, errors.New("file is empty")
	}

	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		decoded, decErr := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
		if decErr != nil {
			return nil, nil, 0, fmt.Errorf("file is not valid UTF-8 or GBK: %w", decErr)
		}
		data = decoded
	}

	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true

	firstRow := true
	for {
		rec, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, warnings, skipped, fmt.Errorf("CSV parse failed: %w", readErr)
		}
		line, _ := r.FieldPos(0)
		if len(rec) == 0 {
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(strings.TrimPrefix(rec[i], "\uFEFF"))
		}

		// A header is optional, but the default file ships with one.
		if firstRow {
			firstRow = false
			if strings.EqualFold(rec[0], "hostName") {
				continue
			}
		}
		if isEmptyRecord(rec) {
			continue
		}
		if len(rec) < 2 {
			warnings = append(warnings, fmt.Sprintf("line %d: expected at least 2 columns, skipped", line))
			skipped++
			continue
		}

		name := rec[0]
		rawIP := rec[1]
		if rawIP == "" {
			warnings = append(warnings, fmt.Sprintf("line %d: hostIP is empty, skipped", line))
			skipped++
			continue
		}
		parsed := net.ParseIP(rawIP)
		v4 := parsed.To4()
		if v4 == nil {
			warnings = append(warnings, fmt.Sprintf("line %d: hostIP %q is not a valid IPv4 address, skipped", line, rawIP))
			skipped++
			continue
		}
		if name == "" {
			name = rawIP
		}

		var addr [4]byte
		copy(addr[:], v4)
		specs = append(specs, hostSpec{
			name: name,
			ip:   rawIP,
			addr: ipv4ToNetworkUint32(addr),
		})
	}

	if len(specs) == 0 {
		return nil, warnings, skipped, errors.New("no valid host records found")
	}
	if len(specs) > maxHosts {
		warnings = append(warnings, fmt.Sprintf("only the first %d hosts are loaded; the rest are ignored", maxHosts))
		specs = specs[:maxHosts]
	}
	return specs, warnings, skipped, nil
}

func isEmptyRecord(rec []string) bool {
	for _, field := range rec {
		if field != "" {
			return false
		}
	}
	return true
}
