/*
File Name:  Download Transfer.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

Temporary download code to provide dummy results for testing. To be replaced!
*/

package webapi

import (
	"bytes"
	"os"
	"time"

	"github.com/PeernetOfficial/core/warehouse"
)

// Starts the download.
func (info *downloadInfo) Start() {
	// current user?
	if bytes.Equal(info.nodeID, info.backend.SelfNodeID()) {
		info.DownloadSelf()
		return
	}

	for n := 0; n < 3 && info.peer == nil; n++ {
		_, info.peer, _ = info.backend.FindNode(info.nodeID, time.Second*5)

		if info.status == DownloadCanceled {
			return
		}
	}

	if info.peer != nil {
		info.Download()
	} else {
		info.status = DownloadCanceled
	}
}

func (info *downloadInfo) Download() {
	//fmt.Printf("Download start of %s\n", hex.EncodeToString(info.hash))

	// try to download the entire file
	reader, fileSize, transferSize, err := FileStartReader(info.peer, info.hash, 0, 0, nil)
	if reader != nil {
		defer reader.Close()
	}
	if err != nil {
		info.status = DownloadCanceled
		return
	} else if fileSize != transferSize {
		info.status = DownloadCanceled
		return
	}

	info.file.Size = fileSize
	info.status = DownloadActive

	// download in a loop
	var fileOffset, totalRead uint64
	dataRemaining := fileSize
	readSize := uint64(4096)

	for dataRemaining > 0 {
		//fmt.Printf("data remaining:  downloaded %d from total %d   = %d %%\n", totalRead, fileSize, totalRead*100/fileSize)
		if dataRemaining < readSize {
			readSize = dataRemaining
		}

		data := make([]byte, readSize)
		n, err := reader.Read(data)

		totalRead += uint64(n)
		dataRemaining -= uint64(n)
		data = data[:n]

		if err != nil {
			info.status = DownloadCanceled
			return
		}

		info.storeDownloadData(data[:n], fileOffset)

		fileOffset += uint64(n)
	}

	//fmt.Printf("data finished:  downloaded %d from total %d   = %d %%\n", totalRead, fileSize, totalRead*100/fileSize)

	info.Finish()
	info.DeleteDefer(time.Hour * 1) // cache the details for 1 hour before removing
}

// Pause pauses the download. Status is DownloadResponseX.
func (info *downloadInfo) Pause() (status int) {
	info.Lock()
	defer info.Unlock()

	if info.status != DownloadActive { // The download must be active to be paused.
		return DownloadResponseActionInvalid
	}

	info.status = DownloadPause

	return DownloadResponseSuccess
}

// Resume resumes the download. Status is DownloadResponseX.
func (info *downloadInfo) Resume() (status int) {
	info.Lock()
	defer info.Unlock()

	if info.status != DownloadPause { // The download must be paused to resume.
		return DownloadResponseActionInvalid
	}

	info.status = DownloadActive

	return DownloadResponseSuccess
}

// Cancel cancels the download. Status is DownloadResponseX.
func (info *downloadInfo) Cancel() (status int) {
	info.Lock()
	defer info.Unlock()

	if info.status >= DownloadCanceled { // The download must not be already canceled or finished.
		return DownloadResponseActionInvalid
	}

	info.status = DownloadCanceled
	info.DiskFile.Handle.Close()

	return DownloadResponseSuccess
}

// Finish marks the download as finished.
func (info *downloadInfo) Finish() (status int) {
	info.Lock()
	defer info.Unlock()

	if info.status != DownloadActive { // The download must be active.
		return DownloadResponseActionInvalid
	}

	info.status = DownloadFinished
	info.DiskFile.Handle.Close()

	return DownloadResponseSuccess
}

// initDiskFile creates the target file
func (info *downloadInfo) initDiskFile(path string) (err error) {
	info.DiskFile.Name = path
	info.DiskFile.Handle, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666) // 666 : All uses can read/write

	return err
}

// storeDownloadData stores downloaded data. It does not change the download status.
func (info *downloadInfo) storeDownloadData(data []byte, offset uint64) (status int) {
	info.Lock()
	defer info.Unlock()

	if info.status != DownloadActive { // The download must be active.
		return DownloadResponseActionInvalid
	}

	if _, err := info.DiskFile.Handle.WriteAt(data, int64(offset)); err != nil {
		return DownloadResponseFileWrite
	}

	info.DiskFile.StoredSize += uint64(len(data))

	return DownloadResponseSuccess
}

func (info *downloadInfo) DownloadSelf() {
	// Check if the file is available in the local warehouse.
	_, fileInfo, status, _ := info.backend.UserWarehouse.FileExists(info.hash)
	if status != warehouse.StatusOK {
		info.status = DownloadCanceled
		return
	}

	info.file.Size = uint64(fileInfo.Size())
	info.status = DownloadActive

	// read the file
	status, bytesRead, _ := info.backend.UserWarehouse.ReadFile(info.hash, 0, int64(fileInfo.Size()), info.DiskFile.Handle)

	info.DiskFile.StoredSize = uint64(bytesRead)

	if status != warehouse.StatusOK {
		info.status = DownloadCanceled
		return
	}

	info.Finish()
	info.DeleteDefer(time.Hour * 1) // cache the details for 1 hour before removing}
}
