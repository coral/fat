// Package mbr provides MBR (Master Boot Record) generation
// with partition table support.
package mbr

import (
	"encoding/binary"

	"fat/geometry"
)

const (
	MBRSize       = 512
	BootCodeSize  = 446
	PartEntrySize = 16
	NumPartitions = 4

	// Partition type codes
	PartTypeFAT32    = 0x0B // FAT32 (CHS addressing)
	PartTypeFAT32LBA = 0x0C // FAT32 with LBA - USE THIS FOR LARGE DISKS
	PartTypeEmpty    = 0x00

	// Boot indicator
	BootIndicatorActive   = 0x80
	BootIndicatorInactive = 0x00

	// MBR signature
	MBRSignature = 0xAA55
)

// PartitionEntry represents a 16-byte partition table entry.
type PartitionEntry struct {
	BootIndicator uint8    // 0x80 = bootable, 0x00 = not bootable
	StartCHS      [3]byte  // CHS address of first sector
	PartitionType uint8    // Partition type code
	EndCHS        [3]byte  // CHS address of last sector
	StartLBA      uint32   // LBA of first sector
	TotalSectors  uint32   // Number of sectors in partition
}

// MBR represents a Master Boot Record.
type MBR struct {
	BootCode   [BootCodeSize]byte
	Partitions [NumPartitions]PartitionEntry
	Signature  uint16
}

// New creates a new MBR with an empty partition table.
func New() *MBR {
	return &MBR{
		Signature: MBRSignature,
	}
}

// SetPartition sets a partition entry at the given index (0-3).
func (m *MBR) SetPartition(index int, entry PartitionEntry) {
	if index >= 0 && index < NumPartitions {
		m.Partitions[index] = entry
	}
}

// NewFAT32Partition creates a FAT32 partition entry.
func NewFAT32Partition(startLBA, totalSectors uint32, geom geometry.Geometry, bootable bool) PartitionEntry {
	entry := PartitionEntry{
		PartitionType: PartTypeFAT32LBA,
		StartLBA:      startLBA,
		TotalSectors:  totalSectors,
	}

	if bootable {
		entry.BootIndicator = BootIndicatorActive
	}

	// Calculate CHS addresses
	entry.StartCHS = geometry.EncodeCHS(startLBA, geom)
	entry.EndCHS = geometry.EncodeCHS(startLBA+totalSectors-1, geom)

	return entry
}

// Serialize converts the MBR to a 512-byte slice in little-endian format.
func (m *MBR) Serialize() []byte {
	buf := make([]byte, MBRSize)

	// Boot code (446 bytes)
	copy(buf[0:BootCodeSize], m.BootCode[:])

	// Partition table (4 entries x 16 bytes = 64 bytes)
	offset := BootCodeSize
	for i := 0; i < NumPartitions; i++ {
		p := &m.Partitions[i]

		buf[offset+0] = p.BootIndicator
		copy(buf[offset+1:offset+4], p.StartCHS[:])
		buf[offset+4] = p.PartitionType
		copy(buf[offset+5:offset+8], p.EndCHS[:])
		binary.LittleEndian.PutUint32(buf[offset+8:offset+12], p.StartLBA)
		binary.LittleEndian.PutUint32(buf[offset+12:offset+16], p.TotalSectors)

		offset += PartEntrySize
	}

	// MBR signature (0xAA55) - note: stored as 55 AA in little-endian
	binary.LittleEndian.PutUint16(buf[510:512], m.Signature)

	return buf
}
