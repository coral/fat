// Package geometry provides CHS (Cylinder-Head-Sector) calculations
// compatible with 86Box's MiniVHD implementation.
package geometry

const SectorSize = 512

// Geometry represents disk CHS geometry.
type Geometry struct {
	Cylinders uint16
	Heads     uint8
	Sectors   uint8 // Sectors per track
}

// TotalSectors returns the total number of sectors for this geometry.
func (g Geometry) TotalSectors() uint64 {
	return uint64(g.Cylinders) * uint64(g.Heads) * uint64(g.Sectors)
}

// TotalBytes returns the total size in bytes for this geometry.
func (g Geometry) TotalBytes() uint64 {
	return g.TotalSectors() * SectorSize
}

// Calculate computes CHS geometry from disk size in bytes.
// This algorithm matches 86Box's MiniVHD implementation exactly.
func Calculate(sizeBytes uint64) Geometry {
	totalSectors := sizeBytes / SectorSize

	// Cap at maximum geometry capacity (65535 * 16 * 255 sectors)
	maxSectors := uint64(65535) * 16 * 255
	if totalSectors > maxSectors {
		totalSectors = maxSectors
	}

	var spt, heads, cyl uint64

	// For large disks (>= 65535 * 16 * 63 sectors), use max SPT and heads
	if totalSectors >= 65535*16*63 {
		spt = 255
		heads = 16
		cth := totalSectors / spt
		cyl = cth / heads
	} else {
		// Smaller disk: start with SPT=17
		spt = 17
		cth := totalSectors / spt

		// Calculate heads: round up cth/1024
		heads = (cth + 1023) / 1024

		// Minimum 4 heads
		if heads < 4 {
			heads = 4
		}

		// If cth >= heads*1024 or heads > 16, upgrade to SPT=31
		if cth >= heads*1024 || heads > 16 {
			spt = 31
			heads = 16
			cth = totalSectors / spt
		}

		// If still too large, upgrade to SPT=63
		if cth >= heads*1024 {
			spt = 63
			heads = 16
			cth = totalSectors / spt
		}

		cyl = cth / heads
	}

	// Cap cylinders at 16-bit max
	if cyl > 65535 {
		cyl = 65535
	}

	return Geometry{
		Cylinders: uint16(cyl),
		Heads:     uint8(heads),
		Sectors:   uint8(spt),
	}
}

// EncodeCHS encodes an LBA address as a 3-byte CHS value for partition tables.
// Format: [Head, Sector | Cyl_hi, Cyl_lo]
// If LBA exceeds CHS addressable range, returns 0xFE, 0xFF, 0xFF (max CHS).
func EncodeCHS(lba uint32, geom Geometry) [3]byte {
	if geom.Heads == 0 || geom.Sectors == 0 {
		return [3]byte{0xFE, 0xFF, 0xFF}
	}

	sectorsPerTrack := uint32(geom.Sectors)
	headsPerCyl := uint32(geom.Heads)

	cylinder := lba / (sectorsPerTrack * headsPerCyl)
	temp := lba % (sectorsPerTrack * headsPerCyl)
	head := temp / sectorsPerTrack
	sector := temp%sectorsPerTrack + 1 // Sectors are 1-indexed

	// If cylinder exceeds 10-bit max (1023), use max CHS
	if cylinder > 1023 {
		return [3]byte{0xFE, 0xFF, 0xFF}
	}

	// Pack into 3 bytes:
	// Byte 0: Head (8 bits)
	// Byte 1: Sector (bits 0-5) | Cylinder high (bits 6-7)
	// Byte 2: Cylinder low (8 bits)
	return [3]byte{
		byte(head),
		byte(sector&0x3F) | byte((cylinder>>8)<<6),
		byte(cylinder & 0xFF),
	}
}
