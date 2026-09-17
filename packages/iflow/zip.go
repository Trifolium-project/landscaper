/*
Copyright © 2022 Aleksandr Ivanov <shamrockspb@gmail.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package iflow

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//Files and folders that must never end up inside an artifact archive
var excludedNames = map[string]bool{
	".DS_Store":   true,
	".git":        true,
	"Thumbs.db":   true,
	"__MACOSX":    true,
	".gitignore":  true,
	".gitkeep":    true,
}

//zipEpoch is the earliest timestamp the zip format can represent. It is used
//for every entry so that packing the same folder twice yields the same bytes.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

//IsZipPath reports whether the path points at an existing regular .zip file
func IsZipPath(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return false
	}

	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}

//IsArtifactDir reports whether the path is a folder holding an iflow project
func IsArtifactDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}

	manifest, err := os.Stat(manifestFilePath(path))
	return err == nil && manifest.Mode().IsRegular()
}

//collectEntries returns the archive paths of every file below srcDir, relative
//to srcDir and using forward slashes, sorted for a deterministic archive.
func collectEntries(srcDir string) ([]string, error) {
	var entries []string

	err := filepath.WalkDir(srcDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if excludedNames[entry.Name()] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.IsDir() {
			return nil
		}

		//Symlinks and other irregular files are not part of an iflow project
		if !entry.Type().IsRegular() {
			return nil
		}

		relative, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		entries = append(entries, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(entries)
	return entries, nil
}

//ZipDir builds an in memory zip archive of an exploded iflow folder. Entries
//are stored relative to srcDir, so META-INF/MANIFEST.MF sits at the archive
//root, which is what the Integration Suite expects.
func ZipDir(srcDir string) ([]byte, error) {
	return ZipDirWithOverrides(srcDir, nil)
}

//ZipDirWithOverrides builds the archive but substitutes the content of the
//given archive paths instead of reading them from disk. This is how an artifact
//is renamed for a target environment without modifying the repository.
func ZipDirWithOverrides(srcDir string, overrides map[string][]byte) ([]byte, error) {
	if !IsArtifactDir(srcDir) {
		return nil, fmt.Errorf("%s is not an integration flow folder, %s is missing", srcDir, ManifestPath)
	}

	entries, err := collectEntries(srcDir)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s contains no files to pack", srcDir)
	}

	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)

	for _, entry := range entries {
		//A fixed timestamp keeps the archive reproducible. Zip cannot store
		//anything earlier than 1980-01-01, and a zero value renders as an
		//invalid date in some tools.
		header := &zip.FileHeader{Name: entry, Method: zip.Deflate, Modified: zipEpoch}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}

		if override, replaced := overrides[entry]; replaced {
			if _, err := writer.Write(override); err != nil {
				return nil, err
			}
			continue
		}

		source, err := os.Open(filepath.Join(srcDir, filepath.FromSlash(entry)))
		if err != nil {
			return nil, err
		}

		_, err = io.Copy(writer, source)
		source.Close()
		if err != nil {
			return nil, err
		}
	}

	if err := archive.Close(); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

//ReadBundleVersionFromZip reads Bundle-Version from a packed artifact
func ReadBundleVersionFromZip(data []byte) (string, error) {
	return readZipHeader(data, HeaderBundleVersion)
}

//ReadBundleNameFromZip reads Bundle-Name from a packed artifact
func ReadBundleNameFromZip(data []byte) (string, error) {
	return readZipHeader(data, HeaderBundleName)
}

//ReadManifestHeaderFromZip reads a single manifest header out of an archive
func ReadManifestHeaderFromZip(data []byte, header string) (string, error) {
	return readZipHeader(data, header)
}

//readZipHeader locates META-INF/MANIFEST.MF inside an archive and reads one header
func readZipHeader(data []byte, header string) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}

	for _, file := range archive.File {
		if !strings.EqualFold(filepath.ToSlash(file.Name), ManifestPath) {
			continue
		}

		reader, err := file.Open()
		if err != nil {
			return "", err
		}

		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return "", err
		}

		return readHeader(content, header)
	}

	return "", fmt.Errorf("%s not found in archive", ManifestPath)
}

//RewriteZipManifest returns the archive with the named manifest headers
//replaced. Needed when a ready zip is uploaded to an environment whose suffix
//has to be applied to Bundle-SymbolicName.
func RewriteZipManifest(data []byte, updates map[string]string) ([]byte, error) {

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	rewritten := false

	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}

		source, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			return nil, err
		}

		if strings.EqualFold(filepath.ToSlash(file.Name), ManifestPath) {
			content, err = setHeaders(content, updates)
			if err != nil {
				return nil, err
			}
			rewritten = true
		}

		header := &zip.FileHeader{Name: file.Name, Method: zip.Deflate, Modified: zipEpoch}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(content); err != nil {
			return nil, err
		}
	}

	if err := archive.Close(); err != nil {
		return nil, err
	}

	if !rewritten {
		return nil, fmt.Errorf("%s not found in archive", ManifestPath)
	}

	return buffer.Bytes(), nil
}

//WriteFileAtomic writes data through a temporary file in the target directory
//and renames it into place, so an interrupted run never leaves a partial file.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()

	cleanup := func() {
		temporary.Close()
		os.Remove(temporaryName)
	}

	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return err
	}

	if err := temporary.Close(); err != nil {
		os.Remove(temporaryName)
		return err
	}

	if err := os.Chmod(temporaryName, mode); err != nil {
		os.Remove(temporaryName)
		return err
	}

	if err := os.Rename(temporaryName, path); err != nil {
		os.Remove(temporaryName)
		return err
	}

	return nil
}
