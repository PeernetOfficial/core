/*
File Username:  Read Package.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package system

import (
	"archive/zip"
	"io/ioutil"
	"path"
	"strings"

	"gopkg.in/ini.v1"
)

type UpdatePackage struct {
	Filename string          // Filename of the update package
	Err      error           // Parsing error if any
	Header   *IniFile        // Header info
	Reader   *zip.ReadCloser // Access to files in the ZIP file
}

// IniFile contains the parsed data from the info.ini file
type IniFile struct {
	Name         string      // Name of the package
	Organization string      // Organization that created this package
	Architecture string      // Target architecture. For example "windows/amd64".
	Actions      []IniAction // Actions to take
}

type IniAction struct {
	Action string `ini:"action"` // Action: extract, delete
	Source string `ini:"source"` // Folder or file in the ZIP file
	Target string `ini:"target"` // Target folder or file. Certain virtual folders such as "%plugins%" are supported.
}

const IniFilename = "info.ini"

// ParseUpdateFiles returns a list of parsed update packages.
// It will check each file in the directory if a ZIP file containing a valid info.ini file.
// The caller must close all returned readers.
func ParseUpdateFiles(Directory string) (files []UpdatePackage, err error) {
	// check all files in the directory
	filesDir, err := ioutil.ReadDir(Directory)
	if err != nil {
		return nil, err
	}

	for _, file := range filesDir {
		if file.IsDir() {
			continue
		}

		filename := file.Name()

		// ZIP file?
		if strings.ToLower(path.Ext(filename)) == ".zip" {
			filenamePath := path.Join(Directory, filename)

			// check if the ZIP archive contains a info.ini file
			reader, err := zip.OpenReader(filenamePath)
			if err != nil { // invalid ZIP file
				continue
			}

			// read info.ini file
			file, err := reader.Open(IniFilename)
			if err != nil {
				continue
			}

			data, err := ioutil.ReadAll(file)
			if err != nil {
				continue
			}

			header, err := ParseIniFile(data)

			files = append(files, UpdatePackage{Filename: filenamePath, Err: err, Header: header, Reader: reader})
		}
	}

	return files, nil
}

func ParseIniFile(data []byte) (header *IniFile, err error) {
	inidata, err := ini.Load(data)
	if err != nil {
		return nil, err
	}

	// parse the main section first
	section, err := inidata.GetSection("main")
	if err != nil {
		return nil, err
	}

	name, err := section.GetKey("name")
	if err != nil {
		return nil, err
	}

	organization, err := section.GetKey("organization")
	if err != nil {
		return nil, err
	}

	architecture, err := section.GetKey("architecture")
	if err != nil {
		return nil, err
	}

	header = &IniFile{
		Name:         name.String(),
		Organization: organization.String(),
		Architecture: architecture.String(),
	}

	// parse any other section
	for _, section := range inidata.Sections() {
		if section.Name() == "main" || section.Name() == "DEFAULT" {
			continue
		}

		var action IniAction

		if section.MapTo(&action) != nil {
			continue
		}

		header.Actions = append(header.Actions, action)
	}

	return header, nil
}
