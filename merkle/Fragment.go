/*
File Name:  Fragment.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package merkle

const KB = 1024
const MB = 1024 * KB
const GB = 1024 * MB
const TB = 1024 * GB
const PB = 1024 * TB

// CalculateFragmentSize calculates the fragment size based on the file size.
func CalculateFragmentSize(fileSize uint64) (fragmentSize uint64) {
	switch {
	case fileSize <= 256*MB:
		return 256 * KB

	case fileSize <= 1*GB:
		return 512 * KB

	case fileSize <= 2*GB:
		return 1 * MB

	case fileSize <= 4*GB:
		return 2 * MB

	case fileSize <= 8*GB:
		return 8 * MB

	case fileSize <= 16*GB:
		return 16 * MB

	case fileSize <= 32*GB:
		return 32 * MB

	case fileSize <= 64*GB:
		return 64 * MB

	case fileSize <= 1*TB:
		return 64 * MB

	case fileSize <= 2*TB:
		return 128 * MB

	case fileSize <= 1*PB:
		return 512 * MB

	default: // 1 PB+
		return 1 * GB
	}
}
