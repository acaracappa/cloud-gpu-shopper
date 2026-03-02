package ssh

import (
	"regexp"
	"strings"
)

// OOMStatus represents parsed dmesg output for OOM killer events
type OOMStatus struct {
	OOMDetected bool
	KilledProcs []string // Process names killed by OOM
	LastOOMTime string   // Timestamp of last OOM event
	RawOutput   string   // Raw dmesg output for debugging
}

// String returns a human-readable summary
func (o *OOMStatus) String() string {
	if !o.OOMDetected {
		return "no OOM events detected"
	}
	msg := "OOM detected"
	if len(o.KilledProcs) > 0 {
		msg += ": killed " + strings.Join(o.KilledProcs, ", ")
	}
	if o.LastOOMTime != "" {
		msg += " (last: " + o.LastOOMTime + ")"
	}
	return msg
}

// killedProcessRe matches dmesg lines like:
//
//	[Thu Feb  6 12:34:56 2026] Killed process 1234 (python3) total-vm:12345kB ...
//	[12345.678] Killed process 1234 (ollama) ...
var killedProcessRe = regexp.MustCompile(`Killed process \d+ \(([^)]+)\)`)

// timestampRe matches dmesg -T timestamp format: [Thu Feb  6 12:34:56 2026]
var timestampRe = regexp.MustCompile(`\[([A-Z][a-z]{2} [A-Z][a-z]{2} +\d+ \d{2}:\d{2}:\d{2} \d{4})\]`)

// ParseOOMOutput parses output from: dmesg -T 2>/dev/null | grep -i "oom\|out of memory\|killed process" | tail -5
func ParseOOMOutput(output string) *OOMStatus {
	output = strings.TrimSpace(output)
	status := &OOMStatus{
		RawOutput: output,
	}

	if output == "" {
		return status
	}

	lines := strings.Split(output, "\n")
	seenProcs := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)

		// Detect OOM events
		if strings.Contains(lower, "oom") || strings.Contains(lower, "out of memory") || strings.Contains(lower, "killed process") {
			status.OOMDetected = true
		}

		// Extract killed process names
		if matches := killedProcessRe.FindStringSubmatch(line); len(matches) >= 2 {
			proc := matches[1]
			if !seenProcs[proc] {
				seenProcs[proc] = true
				status.KilledProcs = append(status.KilledProcs, proc)
			}
		}

		// Extract timestamp from the last matching line
		if ts := timestampRe.FindStringSubmatch(line); len(ts) >= 2 {
			status.LastOOMTime = ts[1]
		}
	}

	return status
}
