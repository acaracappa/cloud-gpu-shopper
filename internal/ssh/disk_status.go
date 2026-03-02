package ssh

import (
	"fmt"
	"strconv"
	"strings"
)

// DiskStatus represents parsed df output for disk usage
type DiskStatus struct {
	Mounts []MountInfo
}

// MountInfo represents disk usage for a single mount point
type MountInfo struct {
	Filesystem string
	TotalGB    float64
	UsedGB     float64
	AvailGB    float64
	UsePct     int
	MountPoint string
}

// IsLow returns true if any mount is above 90% usage
func (d *DiskStatus) IsLow() bool {
	for _, m := range d.Mounts {
		if m.UsePct > 90 {
			return true
		}
	}
	return false
}

// AvailableGB returns available space on the root mount, or the largest mount if root isn't found
func (d *DiskStatus) AvailableGB() float64 {
	if len(d.Mounts) == 0 {
		return 0
	}

	// Prefer root mount
	for _, m := range d.Mounts {
		if m.MountPoint == "/" {
			return m.AvailGB
		}
	}

	// Fall back to mount with most available space
	best := d.Mounts[0].AvailGB
	for _, m := range d.Mounts[1:] {
		if m.AvailGB > best {
			best = m.AvailGB
		}
	}
	return best
}

// String returns a human-readable summary
func (d *DiskStatus) String() string {
	if len(d.Mounts) == 0 {
		return "no mounts"
	}
	parts := make([]string, 0, len(d.Mounts))
	for _, m := range d.Mounts {
		parts = append(parts, fmt.Sprintf("%s: %.1fGB/%.1fGB (%d%%) on %s",
			m.Filesystem, m.UsedGB, m.TotalGB, m.UsePct, m.MountPoint))
	}
	return strings.Join(parts, "; ")
}

// ParseDiskOutput parses output from: df -BG --output=source,size,used,avail,pcent,target | grep -v tmpfs
// Expected format (one header line, then data lines):
//
//	Filesystem      1G-blocks      Used Available Use% Mounted on
//	/dev/sda1            100G       45G       50G  45% /
//	/dev/sdb1            500G      200G      280G  40% /data
//
// Also handles df output without --output flag (standard columns).
func ParseDiskOutput(output string) (*DiskStatus, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return &DiskStatus{}, nil
	}

	lines := strings.Split(output, "\n")
	status := &DiskStatus{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip header lines
		lower := strings.ToLower(line)
		if strings.Contains(lower, "filesystem") || strings.Contains(lower, "mounted on") {
			continue
		}

		mount, err := parseDFLine(line)
		if err != nil {
			continue // Skip unparseable lines
		}
		status.Mounts = append(status.Mounts, mount)
	}

	return status, nil
}

// parseDFLine parses a single line of df output.
// Handles formats like:
//
//	/dev/sda1       100G  45G  50G  45% /
//	/dev/sda1       100   45   50   45% /
func parseDFLine(line string) (MountInfo, error) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return MountInfo{}, fmt.Errorf("expected at least 6 fields, got %d", len(fields))
	}

	info := MountInfo{
		Filesystem: fields[0],
	}

	// Parse size fields - strip trailing 'G' if present
	total, err := parseGBField(fields[1])
	if err != nil {
		return MountInfo{}, fmt.Errorf("parse total: %w", err)
	}
	info.TotalGB = total

	used, err := parseGBField(fields[2])
	if err != nil {
		return MountInfo{}, fmt.Errorf("parse used: %w", err)
	}
	info.UsedGB = used

	avail, err := parseGBField(fields[3])
	if err != nil {
		return MountInfo{}, fmt.Errorf("parse avail: %w", err)
	}
	info.AvailGB = avail

	// Parse percentage - strip trailing '%'
	pctStr := strings.TrimSuffix(fields[4], "%")
	pct, err := strconv.Atoi(pctStr)
	if err != nil {
		return MountInfo{}, fmt.Errorf("parse pct %q: %w", fields[4], err)
	}
	info.UsePct = pct

	// Mount point is the last field (may contain spaces, but typically doesn't)
	info.MountPoint = fields[len(fields)-1]

	return info, nil
}

// parseGBField parses a size field, stripping trailing 'G' suffix
func parseGBField(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "G")
	if s == "" || s == "-" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}
