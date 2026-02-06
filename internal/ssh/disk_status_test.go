package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDiskOutput(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantMounts int
		wantErr    bool
	}{
		{
			name: "standard df -BG output",
			output: `Filesystem      1G-blocks      Used Available Use% Mounted on
/dev/sda1            100G       45G       50G  45% /
/dev/sdb1            500G      200G      280G  40% /data`,
			wantMounts: 2,
		},
		{
			name: "df output without G suffix",
			output: `Filesystem     1G-blocks  Used Available Use% Mounted on
/dev/vda1            50    20       28  40% /`,
			wantMounts: 1,
		},
		{
			name: "single root mount nearly full",
			output: `Filesystem      1G-blocks      Used Available Use% Mounted on
/dev/sda1            100G       95G        2G  95% /`,
			wantMounts: 1,
		},
		{
			name:       "empty output",
			output:     "",
			wantMounts: 0,
		},
		{
			name:       "header only",
			output:     "Filesystem      1G-blocks      Used Available Use% Mounted on",
			wantMounts: 0,
		},
		{
			name: "real vast.ai output",
			output: `Filesystem     1G-blocks  Used Available Use% Mounted on
/dev/sda2           440G  128G      290G  31% /
/dev/sda1             1G    1G        0G 100% /boot/efi`,
			wantMounts: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, err := ParseDiskOutput(tt.output)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, status.Mounts, tt.wantMounts)
		})
	}
}

func TestParseDiskOutput_Values(t *testing.T) {
	output := `Filesystem      1G-blocks      Used Available Use% Mounted on
/dev/sda1            100G       45G       50G  45% /
/dev/sdb1            500G      200G      280G  40% /data`

	status, err := ParseDiskOutput(output)
	require.NoError(t, err)
	require.Len(t, status.Mounts, 2)

	root := status.Mounts[0]
	assert.Equal(t, "/dev/sda1", root.Filesystem)
	assert.Equal(t, 100.0, root.TotalGB)
	assert.Equal(t, 45.0, root.UsedGB)
	assert.Equal(t, 50.0, root.AvailGB)
	assert.Equal(t, 45, root.UsePct)
	assert.Equal(t, "/", root.MountPoint)

	data := status.Mounts[1]
	assert.Equal(t, "/dev/sdb1", data.Filesystem)
	assert.Equal(t, 500.0, data.TotalGB)
	assert.Equal(t, 200.0, data.UsedGB)
	assert.Equal(t, 280.0, data.AvailGB)
	assert.Equal(t, 40, data.UsePct)
	assert.Equal(t, "/data", data.MountPoint)
}

func TestDiskStatus_IsLow(t *testing.T) {
	tests := []struct {
		name   string
		mounts []MountInfo
		want   bool
	}{
		{
			name:   "no mounts",
			mounts: nil,
			want:   false,
		},
		{
			name: "healthy",
			mounts: []MountInfo{
				{UsePct: 45, MountPoint: "/"},
			},
			want: false,
		},
		{
			name: "exactly 90% - not low",
			mounts: []MountInfo{
				{UsePct: 90, MountPoint: "/"},
			},
			want: false,
		},
		{
			name: "above 90% - low",
			mounts: []MountInfo{
				{UsePct: 91, MountPoint: "/"},
			},
			want: true,
		},
		{
			name: "one mount low among many",
			mounts: []MountInfo{
				{UsePct: 30, MountPoint: "/"},
				{UsePct: 95, MountPoint: "/boot"},
				{UsePct: 50, MountPoint: "/data"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiskStatus{Mounts: tt.mounts}
			assert.Equal(t, tt.want, d.IsLow())
		})
	}
}

func TestDiskStatus_AvailableGB(t *testing.T) {
	tests := []struct {
		name   string
		mounts []MountInfo
		want   float64
	}{
		{
			name:   "no mounts",
			mounts: nil,
			want:   0,
		},
		{
			name: "root mount",
			mounts: []MountInfo{
				{AvailGB: 50, MountPoint: "/"},
			},
			want: 50,
		},
		{
			name: "prefers root over larger",
			mounts: []MountInfo{
				{AvailGB: 200, MountPoint: "/data"},
				{AvailGB: 50, MountPoint: "/"},
			},
			want: 50,
		},
		{
			name: "falls back to largest when no root",
			mounts: []MountInfo{
				{AvailGB: 10, MountPoint: "/boot"},
				{AvailGB: 200, MountPoint: "/data"},
			},
			want: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiskStatus{Mounts: tt.mounts}
			assert.Equal(t, tt.want, d.AvailableGB())
		})
	}
}

func TestDiskStatus_String(t *testing.T) {
	d := &DiskStatus{}
	assert.Equal(t, "no mounts", d.String())

	d.Mounts = []MountInfo{
		{Filesystem: "/dev/sda1", TotalGB: 100, UsedGB: 45, AvailGB: 50, UsePct: 45, MountPoint: "/"},
	}
	assert.Contains(t, d.String(), "/dev/sda1")
	assert.Contains(t, d.String(), "45%")
}
