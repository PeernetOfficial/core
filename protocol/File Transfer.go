/*
File Name:  File Transfer.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Encoding of file transfer protocol. Each transfer starts with a header:
Offset  Size   Info
0       8      Total File Size
8       8      Transfer Size
*/

package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

// FileTransferWriteHeader starts writing the header for a file transfer.
func FileTransferWriteHeader(writer io.Writer, fileSize, transferSize uint64) (err error) {
	// Send the header: Total File Size and Transfer Size.
	header := make([]byte, 16)
	binary.LittleEndian.PutUint64(header[0:8], fileSize)
	binary.LittleEndian.PutUint64(header[8:16], transferSize)
	if n, err := writer.Write(header); err != nil {
		return err
	} else if n != len(header) {
		return errors.New("error sending header")
	}

	return nil
}

// FileTransferReadHeader starts reading the header for a file transfer. It will only read the header and keeps the connection open.
func FileTransferReadHeader(reader io.Reader) (fileSize, transferSize uint64, err error) {
	// read the header
	header := make([]byte, 16)
	if n, err := reader.Read(header); err != nil {
		return 0, 0, err
	} else if n != len(header) {
		return 0, 0, errors.New("error reading header")
	}

	fileSize = binary.LittleEndian.Uint64(header[0:8])
	transferSize = binary.LittleEndian.Uint64(header[8:16])

	return fileSize, transferSize, nil
}
