/*
File Name:  File Detection.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/PeernetOfficial/core"
)

// PathToExtension translates a path to a file extension, if possible. It also returns the second file extension if there is one (relevant for files like "test.tar.gz").
func PathToExtension(Path string) (extension, extension2 string, valid bool) {
	_, fileA := path.Split(Path)

	parts := strings.Split(fileA, ".")
	if len(parts) <= 1 {
		return "", "", false
	}
	extension = parts[len(parts)-1]
	extension = strings.ToLower(extension)

	if len(parts) >= 3 {
		extension2 = parts[len(parts)-2]
		extension2 = strings.ToLower(extension2)
	}

	return extension, extension2, true
}

// FileTranslateExtension translates the extension to a File Type and File Format. If invalid, types are 0.
func FileTranslateExtension(extension string) (fileType, fileFormat uint16) {
	switch extension {
	case "txt", "log", "ini", "json", "md":
		return core.TypeText, core.FormatText

	case "csv", "tsv":
		return core.TypeText, core.FormatCSV

	case "html", "htm":
		return core.TypeText, core.FormatHTML

	case "doc", "docx", "rtf", "odt":
		return core.TypeDocument, core.FormatWord

	case "pdf":
		return core.TypeDocument, core.FormatPDF

	case "xls", "xlsx", "ods":
		return core.TypeDocument, core.FormatExcel

	case "gif", "jpg", "jpeg", "png", "svg", "bmp", "tif", "tiff", "jfif":
		return core.TypePicture, core.FormatPicture

	case "mp4", "flv", "avi", "mov", "mpg", "mpeg", "h264", "3g2", "3gp", "mkv", "wmv", "webm":
		return core.TypeVideo, core.FormatVideo

	case "mp3", "ogg", "flac":
		return core.TypeAudio, core.FormatAudio

	case "zip", "rar", "7z", "tar":
		return core.TypeContainer, core.FormatContainer

	case "ppt", "pptx", "odp":
		return core.TypeDocument, core.FormatPowerpoint

	case "epub", "mobi", "prc":
		return core.TypeEbook, core.FormatEbook

	case "gz", "bz", "bz2", "xz":
		return core.TypeCompressed, core.FormatCompressed

	case "sql":
		return core.TypeText, core.FormatDatabase

	case "eml", "mbox":
		return core.TypeText, core.FormatEmail

	case "exe", "sys", "dll", "cmd", "bat":
		return core.TypeExecutable, core.FormatExecutable

	case "msi":
		return core.TypeExecutable, core.FormatInstaller

	case "apk":
		return core.TypeExecutable, core.FormatAPK

	case "iso":
		return core.TypeContainer, core.FormatISO

	default:
		return core.TypeBinary, core.FormatBinary
	}
}

// HTTPContentTypeToCore translates the HTTP content type to the File Type and File Format used by the core package.
func HTTPContentTypeToCore(httpContentType string) (fileType, fileFormat uint16) {
	switch httpContentType {
	case "text/html", "application/xhtml+xml", "application/xml":
		return core.TypeText, core.FormatHTML

	case "text/plain":
		return core.TypeText, core.FormatText

	case "text/csv", "text/tsv", "text/tab-separated-values", "text/x-csv", "application/csv", "application/x-csv", "text/x-comma-separated-values":
		return core.TypeText, core.FormatCSV

	case "application/pdf", "application/x-pdf":
		return core.TypeDocument, core.FormatPDF

	case "application/msword", "application/rtf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.oasis.opendocument.text":
		return core.TypeDocument, core.FormatWord

	case "application/excel", "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.oasis.opendocument.spreadsheet":
		return core.TypeDocument, core.FormatExcel

	case "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "application/vnd.oasis.opendocument.presentation":
		return core.TypeDocument, core.FormatPowerpoint

	case "image/png", "image/jpeg", "image/gif", "image/svg+xml", "image/tiff", "image/webp", "image/bmp", "image/x-bmp", "image/x-windows-bmp": // image/x-icon excluded
		return core.TypePicture, core.FormatPicture

	case "audio/aac", "audio/midi", "audio/ogg", "audio/x-wav", "audio/webm", "audio/3gpp", "audio/3gpp2", "audio/mpeg", "audio/vorbis":
		return core.TypeAudio, core.FormatAudio

	case "video/x-msvideo", "video/x-ms-wmv", "video/mpeg", "video/ogg", "video/webm", "video/3gpp", "video/3gpp2", "video/x-flv", "video/mp4":
		return core.TypeVideo, core.FormatVideo

	case "application/zip", "application/x-rar-compressed", "application/x-tar", "application/x-bzip", "application/x-bzip2", "application/x-7z-compressed":
		return core.TypeContainer, core.FormatContainer

	case "application/epub+zip", "application/x-mobipocket-ebook":
		return core.TypeEbook, core.FormatEbook

	default:
		return core.TypeBinary, core.FormatBinary
	}
}

// FileDataToHTTPContentType returns the HTTP content type based on the initial file data. It reads the first 512 bytes of the file.
func FileDataToHTTPContentType(Path string) (httpContentType string, err error) {
	file, err := os.Open(Path)
	if err != nil {
		return "", err
	}

	// Read up to 512 bytes. This specific number comes from http.DetectContentType which specifies it as constant "sniffLen".
	buff := make([]byte, 512)

	if _, err := file.Read(buff); err != nil {
		return "", err
	}

	if err := file.Close(); err != nil {
		return "", err
	}

	httpContentType = http.DetectContentType(buff)

	// sanitize it first
	httpContentType = strings.ToLower(strings.TrimSpace(httpContentType))
	if indexD := strings.IndexAny(httpContentType, ";"); indexD >= 0 {
		httpContentType = httpContentType[:indexD]
	}

	return httpContentType, nil
}

// FileDetectType detects the File Type and File Format of a file. It uses the extension if available, otherwise the file data, for detection.
func FileDetectType(Path string) (fileType, fileFormat uint16, err error) {
	// If a file extension is available, use that to detect the file type and file format.
	// Otherwise, use the initial file data for detection.
	if ext1, _, valid := PathToExtension(Path); valid {
		fileType, fileFormat = FileTranslateExtension(ext1)
		return fileType, fileFormat, nil
	}

	httpContentType, err := FileDataToHTTPContentType(Path)
	if err != nil {
		return core.TypeBinary, core.FormatBinary, err
	}

	fileType, fileFormat = HTTPContentTypeToCore(httpContentType)
	return fileType, fileFormat, nil
}

type apiResponseFileFormat struct {
	Status     int    `json:"status"`     // Status: 0 = Success, 1 = Error reading file
	FileType   uint16 `json:"filetype"`   // File Type.
	FileFormat uint16 `json:"fileformat"` // File Format.
}

/*
apiFileFormat detects the file type and file format of the specified file.
It will primarily use the file extension for detection. If unavailable, it uses the first 512 bytes of the file data to detect the type.

Request:    GET /file/format?path=[file path on disk]
Result:     200 with JSON structure apiResponseFileFormat
*/
func apiFileFormat(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	filePath := r.Form.Get("path")
	if filePath == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	fileType, fileFormat, err := FileDetectType(filePath)
	if err != nil {
		EncodeJSON(w, r, apiResponseFileFormat{Status: 1})
		return
	}

	EncodeJSON(w, r, apiResponseFileFormat{Status: 0, FileType: fileType, FileFormat: fileFormat})
}
