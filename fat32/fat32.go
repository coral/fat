// Package fat32 provides FAT32 filesystem structure generation
// compatible with Windows 95/98
package fat32

import (
	"crypto/rand"
	"encoding/binary"

	"fat/geometry"
)

const (
	SectorSize      = 512
	ReservedSectors = 32 // Standard for FAT32
	NumFATs         = 2
	RootCluster     = 2 // First cluster is always 2

	// FSInfo signatures
	FSInfoLeadSig   = 0x41615252
	FSInfoStructSig = 0x61417272
	FSInfoTrailSig  = 0xAA550000

	// FAT special values
	FATMedia   = 0x0FFFFFF8 // Media descriptor in FAT[0]
	FATEOCMark = 0x0FFFFFFF // End of chain marker
)

// FAT32 represents FAT32 filesystem parameters and state.
type FAT32 struct {
	// Geometry
	TotalSectors    uint32
	HiddenSectors   uint32 // Sectors before partition (usually 63)
	SectorsPerTrack uint16
	NumHeads        uint16

	// Calculated values
	SectorsPerCluster uint8
	SectorsPerFAT     uint32
	TotalClusters     uint32
	DataStartSector   uint32 // Relative to partition start

	// Volume info
	VolumeID    uint32
	VolumeLabel [11]byte
}

// New creates a new FAT32 filesystem configuration.
func New(partitionSectors, hiddenSectors uint32, geom geometry.Geometry) *FAT32 {
	f := &FAT32{
		TotalSectors:    partitionSectors,
		HiddenSectors:   hiddenSectors,
		SectorsPerTrack: uint16(geom.Sectors),
		NumHeads:        uint16(geom.Heads),
	}

	// Calculate sectors per cluster based on partition size
	f.SectorsPerCluster = calculateSectorsPerCluster(partitionSectors)

	// Calculate FAT size
	dataSectors := partitionSectors - ReservedSectors
	totalClusters := dataSectors / uint32(f.SectorsPerCluster)

	fatEntries := totalClusters + 2
	fatBytes := fatEntries * 4
	f.SectorsPerFAT = (fatBytes + SectorSize - 1) / SectorSize

	// Recalculate actual data sectors and clusters
	dataSectors = partitionSectors - ReservedSectors - (NumFATs * f.SectorsPerFAT)
	f.TotalClusters = dataSectors / uint32(f.SectorsPerCluster)

	// Data region starts after reserved + FATs
	f.DataStartSector = ReservedSectors + (NumFATs * f.SectorsPerFAT)

	// Generate random volume ID
	var volID [4]byte
	rand.Read(volID[:])
	f.VolumeID = binary.LittleEndian.Uint32(volID[:])

	// Volume label
	copy(f.VolumeLabel[:], "WIN98      ")

	return f
}

// calculateSectorsPerCluster determines cluster size based on partition size.
func calculateSectorsPerCluster(totalSectors uint32) uint8 {
	sizeBytes := uint64(totalSectors) * SectorSize

	switch {
	case sizeBytes <= 64*1024*1024:
		return 1
	case sizeBytes <= 128*1024*1024:
		return 2
	case sizeBytes <= 256*1024*1024:
		return 4
	case sizeBytes <= 8*1024*1024*1024:
		return 8
	case sizeBytes <= 16*1024*1024*1024:
		return 16
	case sizeBytes <= 32*1024*1024*1024:
		return 32
	default:
		return 64
	}
}

// MakeBootSector creates a FAT32 boot sector (VBR).
func (f *FAT32) MakeBootSector() []byte {
	buf := make([]byte, SectorSize)

	// Jump instruction
	buf[0] = 0xEB
	buf[1] = 0x58
	buf[2] = 0x90

	// OEM Name
	copy(buf[3:11], "MSDOS5.0")

	// BIOS Parameter Block
	binary.LittleEndian.PutUint16(buf[11:13], SectorSize)
	buf[13] = f.SectorsPerCluster
	binary.LittleEndian.PutUint16(buf[14:16], ReservedSectors)
	buf[16] = NumFATs
	binary.LittleEndian.PutUint16(buf[17:19], 0) // Root entry count (0 for FAT32)
	binary.LittleEndian.PutUint16(buf[19:21], 0) // Total sectors 16-bit
	buf[21] = 0xF8                               // Media descriptor
	binary.LittleEndian.PutUint16(buf[22:24], 0) // Sectors per FAT 16-bit
	binary.LittleEndian.PutUint16(buf[24:26], f.SectorsPerTrack)
	binary.LittleEndian.PutUint16(buf[26:28], f.NumHeads)
	binary.LittleEndian.PutUint32(buf[28:32], f.HiddenSectors)
	binary.LittleEndian.PutUint32(buf[32:36], f.TotalSectors)

	// FAT32 Extended BPB
	binary.LittleEndian.PutUint32(buf[36:40], f.SectorsPerFAT)
	binary.LittleEndian.PutUint16(buf[40:42], 0) // Extended flags
	binary.LittleEndian.PutUint16(buf[42:44], 0) // FS version
	binary.LittleEndian.PutUint32(buf[44:48], RootCluster)
	binary.LittleEndian.PutUint16(buf[48:50], 1) // FSInfo sector
	binary.LittleEndian.PutUint16(buf[50:52], 6) // Backup boot sector

	buf[64] = 0x80 // Drive number
	buf[65] = 0    // Reserved
	buf[66] = 0x29 // Extended boot signature

	binary.LittleEndian.PutUint32(buf[67:71], f.VolumeID)
	copy(buf[71:82], f.VolumeLabel[:])
	copy(buf[82:90], "FAT32   ")

	// Boot signature
	binary.LittleEndian.PutUint16(buf[510:512], 0xAA55)

	return buf
}

// MakeFSInfo creates an FSInfo sector.
func (f *FAT32) MakeFSInfo() []byte {
	buf := make([]byte, SectorSize)

	binary.LittleEndian.PutUint32(buf[0:4], FSInfoLeadSig)
	binary.LittleEndian.PutUint32(buf[484:488], FSInfoStructSig)

	// Free cluster count
	freeCount := f.TotalClusters - 1
	binary.LittleEndian.PutUint32(buf[488:492], freeCount)

	// Next free cluster hint
	binary.LittleEndian.PutUint32(buf[492:496], 3)

	binary.LittleEndian.PutUint32(buf[508:512], FSInfoTrailSig)

	return buf
}

// MakeFATSector creates a FAT sector at the given offset within the FAT.
func (f *FAT32) MakeFATSector(sectorOffset uint32) []byte {
	buf := make([]byte, SectorSize)

	if sectorOffset == 0 {
		// First sector of FAT contains special entries
		// FAT[0]: Media descriptor
		binary.LittleEndian.PutUint32(buf[0:4], FATMedia)
		// FAT[1]: End of chain marker
		binary.LittleEndian.PutUint32(buf[4:8], FATEOCMark)
		// FAT[2]: Root directory - end of chain
		binary.LittleEndian.PutUint32(buf[8:12], FATEOCMark)
	}
	// All other entries are 0 (free)

	return buf
}

// MakeRootDirectory creates the first sector of the root directory.
func (f *FAT32) MakeRootDirectory() []byte {
	buf := make([]byte, SectorSize)

	// Volume label entry
	copy(buf[0:11], f.VolumeLabel[:])
	buf[11] = 0x08 // Attribute: Volume Label

	return buf
}

// GetDataStartSector returns the sector offset of data region from partition start.
func (f *FAT32) GetDataStartSector() uint32 {
	return f.DataStartSector
}
