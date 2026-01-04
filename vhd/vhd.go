// Package vhd provides VHD (Virtual Hard Disk) generation
// compatible with Microsoft VHD specification and 86Box.
// Creates Dynamic/Sparse VHDs that only allocate space on write.
package vhd

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"time"

	"fat/geometry"
)

const (
	FooterSize       = 512
	SparseHeaderSize = 1024
	SectorSize       = 512

	// VHD timestamp epoch: January 1, 2000 00:00:00 UTC
	TimestampEpoch = 946684800

	DiskTypeFixed        = 2
	DiskTypeDynamic      = 3
	DiskTypeDifferencing = 4

	// Default block size: 2MB
	DefaultBlockSize = 2 * 1024 * 1024

	// Unallocated BAT entry
	BATEntryUnallocated = 0xFFFFFFFF
)

// Footer represents a VHD footer structure (512 bytes, big-endian).
type Footer struct {
	Cookie         [8]byte
	Features       uint32
	FileFormatVer  uint32
	DataOffset     uint64
	Timestamp      uint32
	CreatorApp     [4]byte
	CreatorVersion uint32
	CreatorHostOS  [4]byte
	OriginalSize   uint64
	CurrentSize    uint64
	DiskGeometry   uint32
	DiskType       uint32
	Checksum       uint32
	UUID           [16]byte
	SavedState     uint8
	Reserved       [427]byte
}

// SparseHeader represents the dynamic disk header (1024 bytes, big-endian).
type SparseHeader struct {
	Cookie          [8]byte   // "cxsparse"
	DataOffset      uint64    // 0xFFFFFFFFFFFFFFFF (unused)
	BATOffset       uint64    // Offset to BAT
	HeaderVersion   uint32    // 0x00010000
	MaxBATEntries   uint32    // Number of blocks
	BlockSize       uint32    // Block size in bytes (default 2MB)
	Checksum        uint32    // One's complement checksum
	ParentUUID      [16]byte  // Parent UUID (zeros for non-diff)
	ParentTimestamp uint32    // Parent timestamp
	Reserved1       uint32    // Reserved
	ParentName      [512]byte // Parent filename (UTF-16 BE)
	ParentLocator   [192]byte // 8 parent locator entries (24 bytes each)
	Reserved2       [256]byte // Reserved
}

// DynamicVHD represents a dynamic/sparse VHD.
type DynamicVHD struct {
	Footer       *Footer
	SparseHeader *SparseHeader
	BlockSize    uint32
	MaxBATEntries uint32
}

// NewDynamicVHD creates a new dynamic/sparse VHD.
func NewDynamicVHD(sizeBytes uint64, geom geometry.Geometry) *DynamicVHD {
	blockSize := uint32(DefaultBlockSize)
	maxBATEntries := uint32((sizeBytes + uint64(blockSize) - 1) / uint64(blockSize))

	// BAT offset is right after footer copy + sparse header
	batOffset := uint64(FooterSize + SparseHeaderSize)

	vhd := &DynamicVHD{
		BlockSize:     blockSize,
		MaxBATEntries: maxBATEntries,
	}

	// Create footer
	vhd.Footer = &Footer{}
	copy(vhd.Footer.Cookie[:], "conectix")
	vhd.Footer.Features = 0x00000002
	vhd.Footer.FileFormatVer = 0x00010000
	vhd.Footer.DataOffset = uint64(FooterSize) // Points to sparse header
	vhd.Footer.Timestamp = uint32(time.Now().Unix() - TimestampEpoch)
	copy(vhd.Footer.CreatorApp[:], "govh")
	vhd.Footer.CreatorVersion = 0x00010000
	copy(vhd.Footer.CreatorHostOS[:], "Wi2k")
	vhd.Footer.OriginalSize = sizeBytes
	vhd.Footer.CurrentSize = sizeBytes
	vhd.Footer.DiskGeometry = (uint32(geom.Cylinders) << 16) | (uint32(geom.Heads) << 8) | uint32(geom.Sectors)
	vhd.Footer.DiskType = DiskTypeDynamic
	rand.Read(vhd.Footer.UUID[:])

	// Create sparse header
	vhd.SparseHeader = &SparseHeader{}
	copy(vhd.SparseHeader.Cookie[:], "cxsparse")
	vhd.SparseHeader.DataOffset = 0xFFFFFFFFFFFFFFFF
	vhd.SparseHeader.BATOffset = batOffset
	vhd.SparseHeader.HeaderVersion = 0x00010000
	vhd.SparseHeader.MaxBATEntries = maxBATEntries
	vhd.SparseHeader.BlockSize = blockSize

	return vhd
}

// SerializeFooter converts the footer to a 512-byte slice.
func (v *DynamicVHD) SerializeFooter() []byte {
	buf := make([]byte, FooterSize)

	copy(buf[0:8], v.Footer.Cookie[:])
	binary.BigEndian.PutUint32(buf[8:12], v.Footer.Features)
	binary.BigEndian.PutUint32(buf[12:16], v.Footer.FileFormatVer)
	binary.BigEndian.PutUint64(buf[16:24], v.Footer.DataOffset)
	binary.BigEndian.PutUint32(buf[24:28], v.Footer.Timestamp)
	copy(buf[28:32], v.Footer.CreatorApp[:])
	binary.BigEndian.PutUint32(buf[32:36], v.Footer.CreatorVersion)
	copy(buf[36:40], v.Footer.CreatorHostOS[:])
	binary.BigEndian.PutUint64(buf[40:48], v.Footer.OriginalSize)
	binary.BigEndian.PutUint64(buf[48:56], v.Footer.CurrentSize)
	binary.BigEndian.PutUint32(buf[56:60], v.Footer.DiskGeometry)
	binary.BigEndian.PutUint32(buf[60:64], v.Footer.DiskType)
	// Checksum at 64:68 - set to 0 for calculation
	copy(buf[68:84], v.Footer.UUID[:])
	buf[84] = v.Footer.SavedState

	// Calculate and insert checksum
	checksum := calculateChecksum(buf)
	binary.BigEndian.PutUint32(buf[64:68], checksum)

	return buf
}

// SerializeSparseHeader converts the sparse header to a 1024-byte slice.
func (v *DynamicVHD) SerializeSparseHeader() []byte {
	buf := make([]byte, SparseHeaderSize)

	copy(buf[0:8], v.SparseHeader.Cookie[:])
	binary.BigEndian.PutUint64(buf[8:16], v.SparseHeader.DataOffset)
	binary.BigEndian.PutUint64(buf[16:24], v.SparseHeader.BATOffset)
	binary.BigEndian.PutUint32(buf[24:28], v.SparseHeader.HeaderVersion)
	binary.BigEndian.PutUint32(buf[28:32], v.SparseHeader.MaxBATEntries)
	binary.BigEndian.PutUint32(buf[32:36], v.SparseHeader.BlockSize)
	// Checksum at 36:40 - set to 0 for calculation
	copy(buf[40:56], v.SparseHeader.ParentUUID[:])
	binary.BigEndian.PutUint32(buf[56:60], v.SparseHeader.ParentTimestamp)
	binary.BigEndian.PutUint32(buf[60:64], v.SparseHeader.Reserved1)
	copy(buf[64:576], v.SparseHeader.ParentName[:])
	copy(buf[576:768], v.SparseHeader.ParentLocator[:])

	// Calculate and insert checksum
	checksum := calculateSparseChecksum(buf)
	binary.BigEndian.PutUint32(buf[36:40], checksum)

	return buf
}

// WriteBAT writes the Block Allocation Table (all entries unallocated).
func (v *DynamicVHD) WriteBAT(w io.Writer) error {
	// Each BAT entry is 4 bytes (uint32), all set to 0xFFFFFFFF (unallocated)
	// BAT must be sector-aligned, so round up to nearest sector
	batBytes := v.MaxBATEntries * 4
	batSectors := (batBytes + SectorSize - 1) / SectorSize
	batSize := batSectors * SectorSize

	buf := make([]byte, batSize)
	// Fill with 0xFF (unallocated)
	for i := range buf {
		buf[i] = 0xFF
	}

	_, err := w.Write(buf)
	return err
}

// BATSize returns the size of the BAT in bytes (sector-aligned).
func (v *DynamicVHD) BATSize() uint32 {
	batBytes := v.MaxBATEntries * 4
	batSectors := (batBytes + SectorSize - 1) / SectorSize
	return batSectors * SectorSize
}

// calculateChecksum computes the one's complement checksum of the footer.
func calculateChecksum(data []byte) uint32 {
	var sum uint32
	for _, b := range data {
		sum += uint32(b)
	}
	return ^sum
}

// calculateSparseChecksum computes the one's complement checksum of the sparse header.
func calculateSparseChecksum(data []byte) uint32 {
	var sum uint32
	for _, b := range data {
		sum += uint32(b)
	}
	return ^sum
}
