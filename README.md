# FAT 

## Small tool to create a VHD image for 86box

Installing Windows 98 is a trip and reality is I don't have the energy to fight the sloppy code of MSFT anno 1997 that went into the `fdisk` of Windows 98. FAT32 was just new and `fdisk` is riddled with bugs.

Was easier to just write a separate tool to generate a decently sized image with the right FAT32 partition in a sparse image.

## Usage

`go run fat.go`

That's it, it gives you a sparse .vhd that holds up to ~60GB. If else is desired you can set `--size 40` to get 40 GB.

## AI DISCLAIMER

this is fully slopped. no regrets.