/*
File Name:  Execute.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package system

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Execute the actions described in the info header
func (update *UpdatePackage) Execute(DataFolder, PluginFolder string) (err error) {
	for _, action := range update.Header.Actions {
		switch action.Action {
		case "extract":
			destination := resolveFolders(action.Target, DataFolder, PluginFolder)

			for _, f := range update.Reader.File {
				// filter the filename based on source
				if !strings.HasPrefix(f.Name, action.Source) {
					continue
				}

				err := unzipFile(f, destination)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Deletes the update package file
func (update *UpdatePackage) Delete() (err error) {
	return os.Remove(update.Filename)
}

func unzipFile(f *zip.File, destination string) error {
	// 4. Check if file paths are not vulnerable to Zip Slip
	filePath := filepath.Join(destination, f.Name)
	if !strings.HasPrefix(filePath, filepath.Clean(destination)+string(os.PathSeparator)) {
		return errors.New("invalid file path: " + filePath)
	}

	// 5. Create directory tree
	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		return err
	}

	// 6. Create a destination file for unzipped content
	destinationFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	// 7. Unzip the content of a file and copy it to the destination file
	zippedFile, err := f.Open()
	if err != nil {
		return err
	}
	defer zippedFile.Close()

	if _, err := io.Copy(destinationFile, zippedFile); err != nil {
		return err
	}
	return nil
}

func resolveFolders(folder, dataFolder, pluginFolder string) (resolved string) {
	resolved = strings.Replace(folder, "%plugin%", pluginFolder, 1)
	resolved = strings.Replace(resolved, "%data%", dataFolder, 1)

	return resolved
}
