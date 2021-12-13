/*
File Name:  Download Temp.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

Temporary download code to provide dummy results for testing. To be replaced!
*/

package webapi

import (
	"math/rand"
	"os"
	"time"
)

// Start is a dummy downloader
func (info *downloadInfo) Start() {
	time.Sleep(time.Second * time.Duration(rand.Intn(5)))

	// request metadata
	//info.file = blockRecordFileToAPI(createTestResult(-1))

	// join swarm

	info.status = DownloadActive

	// start download
	for n := uint64(0); n < 10; n++ {
		time.Sleep(time.Second * time.Duration(rand.Intn(5)))

		randomData := make([]byte, info.file.Size/10)
		rand.Read(randomData)

		info.storeDownloadData(randomData, n*info.file.Size/10)
	}

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

	//if _, err := info.DiskFile.Handle.Seek(int64(offset), 0); err != nil {
	//	return err
	//}

	if _, err := info.DiskFile.Handle.WriteAt(data, int64(offset)); err != nil {
		return DownloadResponseFileWrite
	}

	info.DiskFile.StoredSize += uint64(len(data))

	return DownloadResponseSuccess
}
