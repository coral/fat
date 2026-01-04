// VHD Generator for Windows 98 / 86Box
//
// Creates a sparse VHD file with MBR partition table and
// FAT32 filesystem, compatible with Windows 98/ME and 86Box emulator.
// The file is sparse - only allocated blocks take disk space.

package main

import (
	"flag"
	"fmt"
	"os"

	"fat/fat32"
	"fat/geometry"
	"fat/mbr"
	"fat/vhd"
)

const (
	SectorSize        = 512
	DefaultDiskSizeGB = 60
	PartitionStartLBA = 63              // Standard partition alignment
	BlockSize         = 2 * 1024 * 1024 // 2MB blocks
	SectorsPerBlock   = BlockSize / SectorSize
)

func main() {
	// Command line flags
	outputFile := flag.String("o", "disk.vhd", "Output VHD filename")
	sizeGB := flag.Int("size", DefaultDiskSizeGB, "Disk size in GB")
	flag.Parse()

	diskSizeBytes := uint64(*sizeGB) * 1024 * 1024 * 1024

	fmt.Printf("Creating %dGB Sparse VHD: %s\n", *sizeGB, *outputFile)

	// Calculate geometry using 86Box's algorithm
	geom := geometry.Calculate(diskSizeBytes)
	fmt.Printf("Geometry: %d cylinders, %d heads, %d sectors/track\n",
		geom.Cylinders, geom.Heads, geom.Sectors)

	// Actual size based on geometry
	actualSize := geom.TotalBytes()
	totalSectors := actualSize / SectorSize
	fmt.Printf("Virtual size: %d bytes (%d sectors)\n", actualSize, totalSectors)

	// Partition parameters
	partitionSectors := uint32(totalSectors) - PartitionStartLBA
	fmt.Printf("Partition: starts at LBA %d, %d sectors\n", PartitionStartLBA, partitionSectors)

	// Create dynamic VHD
	dynVHD := vhd.NewDynamicVHD(actualSize, geom)
	fmt.Printf("Block size: %d bytes, BAT entries: %d\n", dynVHD.BlockSize, dynVHD.MaxBATEntries)

	// Initialize FAT32 to calculate sizes
	fat := fat32.New(partitionSectors, PartitionStartLBA, geom)
	fmt.Printf("FAT32: %d sectors/cluster, %d sectors/FAT, %d clusters\n",
		fat.SectorsPerCluster, fat.SectorsPerFAT, fat.TotalClusters)

	// Calculate how many blocks we need to allocate for filesystem metadata
	// We need: MBR (sector 0), partition gap, boot sector, FSInfo, reserved, FATs, root dir
	dataStartSector := PartitionStartLBA + fat.GetDataStartSector() + uint32(fat.SectorsPerCluster)
	lastMetadataSector := dataStartSector - 1
	numBlocksNeeded := (lastMetadataSector / SectorsPerBlock) + 1
	fmt.Printf("Metadata ends at sector %d, allocating %d blocks\n", lastMetadataSector, numBlocksNeeded)

	// Create output file
	f, err := os.Create(*outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	// Write footer copy at start
	fmt.Println("Writing footer copy...")
	if _, err := f.Write(dynVHD.SerializeFooter()); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing footer copy: %v\n", err)
		os.Exit(1)
	}

	// Write sparse header
	fmt.Println("Writing sparse header...")
	if _, err := f.Write(dynVHD.SerializeSparseHeader()); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing sparse header: %v\n", err)
		os.Exit(1)
	}

	// Create and write BAT
	fmt.Println("Writing BAT...")
	batSize := dynVHD.BATSize()
	bat := make([]byte, batSize)
	// Initialize all entries to unallocated (0xFFFFFFFF)
	for i := 0; i < len(bat); i++ {
		bat[i] = 0xFF
	}

	// Data blocks start after: footer copy + sparse header + BAT
	dataBlocksStart := uint32(vhd.FooterSize + vhd.SparseHeaderSize + batSize)
	// Round up to sector boundary
	dataBlocksStart = ((dataBlocksStart + SectorSize - 1) / SectorSize) * SectorSize

	// Allocate BAT entries for metadata blocks
	// Each BAT entry is the sector offset of the block (including its bitmap)
	bitmapSectors := uint32(1) // 512 bytes bitmap for 2MB block = 1 sector
	blockTotalSectors := SectorsPerBlock + bitmapSectors

	for i := uint32(0); i < numBlocksNeeded; i++ {
		blockOffset := dataBlocksStart + (i * blockTotalSectors * SectorSize)
		// BAT entry is sector number, not byte offset
		batEntrySector := blockOffset / SectorSize
		// Write as big-endian
		bat[i*4+0] = byte(batEntrySector >> 24)
		bat[i*4+1] = byte(batEntrySector >> 16)
		bat[i*4+2] = byte(batEntrySector >> 8)
		bat[i*4+3] = byte(batEntrySector)
	}

	if _, err := f.Write(bat); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing BAT: %v\n", err)
		os.Exit(1)
	}

	// Pad to dataBlocksStart if needed
	currentPos := vhd.FooterSize + vhd.SparseHeaderSize + int(batSize)
	if currentPos < int(dataBlocksStart) {
		padding := make([]byte, int(dataBlocksStart)-currentPos)
		if _, err := f.Write(padding); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing padding: %v\n", err)
			os.Exit(1)
		}
	}

	// Create disk image buffer for metadata sectors
	// We'll build the complete first N blocks worth of data
	fmt.Println("Building filesystem structures...")

	// Prepare MBR
	mbrData := mbr.New()
	partition := mbr.NewFAT32Partition(PartitionStartLBA, partitionSectors, geom, true)
	mbrData.SetPartition(0, partition)
	mbrBytes := mbrData.Serialize()

	// Write allocated blocks
	fmt.Println("Writing data blocks...")
	for blockNum := uint32(0); blockNum < numBlocksNeeded; blockNum++ {
		// Each block has: bitmap (1 sector) + data (4096 sectors)
		blockData := make([]byte, (bitmapSectors+SectorsPerBlock)*SectorSize)

		// Set all bits in bitmap to 1 (all sectors present)
		for i := uint32(0); i < bitmapSectors*SectorSize; i++ {
			blockData[i] = 0xFF
		}

		// Fill in sector data for this block
		startSector := blockNum * SectorsPerBlock
		endSector := startSector + SectorsPerBlock

		for sector := startSector; sector < endSector; sector++ {
			dataOffset := bitmapSectors*SectorSize + (sector-startSector)*SectorSize

			if sector == 0 {
				// MBR
				copy(blockData[dataOffset:dataOffset+SectorSize], mbrBytes)
			} else if sector >= PartitionStartLBA {
				// Partition data
				partSector := sector - PartitionStartLBA

				if partSector == 0 {
					// FAT32 boot sector
					bootSector := fat.MakeBootSector()
					copy(blockData[dataOffset:dataOffset+SectorSize], bootSector)
				} else if partSector == 1 {
					// FSInfo
					fsInfo := fat.MakeFSInfo()
					copy(blockData[dataOffset:dataOffset+SectorSize], fsInfo)
				} else if partSector == 6 {
					// Backup boot sector
					bootSector := fat.MakeBootSector()
					copy(blockData[dataOffset:dataOffset+SectorSize], bootSector)
				} else if partSector == 7 {
					// Backup FSInfo
					fsInfo := fat.MakeFSInfo()
					copy(blockData[dataOffset:dataOffset+SectorSize], fsInfo)
				} else if partSector >= fat32.ReservedSectors && partSector < fat32.ReservedSectors+fat.SectorsPerFAT {
					// FAT1
					fatOffset := partSector - fat32.ReservedSectors
					fatData := fat.MakeFATSector(fatOffset)
					copy(blockData[dataOffset:dataOffset+SectorSize], fatData)
				} else if partSector >= fat32.ReservedSectors+fat.SectorsPerFAT && partSector < fat32.ReservedSectors+2*fat.SectorsPerFAT {
					// FAT2
					fatOffset := partSector - fat32.ReservedSectors - fat.SectorsPerFAT
					fatData := fat.MakeFATSector(fatOffset)
					copy(blockData[dataOffset:dataOffset+SectorSize], fatData)
				} else if partSector >= fat.GetDataStartSector() && partSector < fat.GetDataStartSector()+uint32(fat.SectorsPerCluster) {
					// Root directory (first cluster)
					clusterOffset := partSector - fat.GetDataStartSector()
					if clusterOffset == 0 {
						rootDir := fat.MakeRootDirectory()
						copy(blockData[dataOffset:dataOffset+SectorSize], rootDir)
					}
					// Other sectors in root cluster are zeros (already)
				}
				// Other sectors remain zero
			}
			// Sectors 1-62 (partition gap) remain zero
		}

		if _, err := f.Write(blockData); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing block %d: %v\n", blockNum, err)
			os.Exit(1)
		}
	}

	// Write footer at end
	fmt.Println("Writing footer...")
	if _, err := f.Write(dynVHD.SerializeFooter()); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing footer: %v\n", err)
		os.Exit(1)
	}

	// Get final file size
	fi, _ := f.Stat()
	fmt.Printf("\nCreated %s\n", *outputFile)
	fmt.Printf("File size on disk: %d bytes (%.2f MB)\n", fi.Size(), float64(fi.Size())/(1024*1024))
	fmt.Printf("Virtual disk size: %d bytes (%.2f GB)\n", actualSize, float64(actualSize)/(1024*1024*1024))
}
