/*
File Name:  File Formats.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Definition of all recognized file formats. This file is likely being updated more frequently than regular code.
*/

package core

// General content types of data.
const (
	TypeBinary     = iota // Binary/unspecified
	TypeText              // Plain text
	TypePicture           // Picture of any format
	TypeVideo             // Video
	TypeAudio             // Audio
	TypeDocument          // Any document file, including office documents, PDFs, power point, spreadsheets
	TypeExecutable        // Any executable file, OS independent
	TypeContainer         // Container files like ZIP, RAR, TAR, ISO
	TypeCompressed        // Compressed files like GZ, BZ
	TypeFolder            // Virtual folder
	TypeEbook             // Ebook
)

// File formats. New ones may be added to the list as required.
const (
	FormatBinary        = iota // Binary/unspecified
	FormatPDF                  // PDF document
	FormatWord                 // Word document
	FormatExcel                // Excel
	FormatPowerpoint           // Powerpoint
	FormatPicture              // Pictures (including GIF, excluding icons)
	FormatAudio                // Audio files
	FormatVideo                // Video files
	FormatContainer            // Compressed files including ZIP, RAR, TAR and others
	FormatHTML                 // HTML file
	FormatText                 // Text file
	FormatEbook                // Ebook file
	FormatCompressed           // Compressed file
	FormatDatabase             // Database file
	FormatEmail                // Single email
	FormatCSV                  // CSV file
	FormatFolder               // Virtual folder
	FormatExecutable           // Executable file
	FormatInstaller            // Installer
	FormatAPK                  // APK
	FormatISO                  // ISO
	FormatPeernetSearch        // Peernet Search
)
