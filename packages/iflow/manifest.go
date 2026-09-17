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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//ManifestPath is the location of the bundle manifest inside an iflow project
const ManifestPath = "META-INF/MANIFEST.MF"

//Manifest headers read or written by landscaper
const (
	HeaderBundleVersion      = "Bundle-Version"
	HeaderBundleSymbolicName = "Bundle-SymbolicName"
	HeaderBundleName         = "Bundle-Name"
)

//manifestLine is a single physical line of a manifest, with its terminator kept
//verbatim so that a rewrite does not convert CRLF to LF or vice versa.
type manifestLine struct {
	Content    string
	Terminator string
}

//splitManifestLines splits raw manifest bytes into physical lines, preserving
//the original line terminators (SAP exports MANIFEST.MF with CRLF).
func splitManifestLines(data []byte) []manifestLine {
	var lines []manifestLine

	text := string(data)
	for len(text) > 0 {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			lines = append(lines, manifestLine{Content: text})
			break
		}

		content := text[:index]
		terminator := "\n"
		if strings.HasSuffix(content, "\r") {
			content = strings.TrimSuffix(content, "\r")
			terminator = "\r\n"
		}

		lines = append(lines, manifestLine{Content: content, Terminator: terminator})
		text = text[index+1:]
	}

	return lines
}

//isContinuation reports whether a physical line continues the previous header.
//Per the JAR specification continuation lines start with a single space.
func isContinuation(content string) bool {
	return strings.HasPrefix(content, " ")
}

//headerIndex returns the index of the physical line starting the given header,
//and the number of continuation lines that follow it. Header names are
//case insensitive. Returns -1 when the header is absent.
func headerIndex(lines []manifestLine, header string) (int, int) {
	prefix := strings.ToLower(header) + ":"

	for index, line := range lines {
		if isContinuation(line.Content) {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(line.Content), prefix) {
			continue
		}

		continuations := 0
		for next := index + 1; next < len(lines) && isContinuation(lines[next].Content); next++ {
			continuations++
		}
		return index, continuations
	}

	return -1, 0
}

//readHeader joins a header and its continuation lines into a single value
func readHeader(data []byte, header string) (string, error) {
	lines := splitManifestLines(data)

	index, continuations := headerIndex(lines, header)
	if index < 0 {
		return "", fmt.Errorf("header %s not found in manifest", header)
	}

	value := strings.TrimPrefix(lines[index].Content[len(header)+1:], " ")
	for offset := 1; offset <= continuations; offset++ {
		value += strings.TrimPrefix(lines[index+offset].Content, " ")
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("header %s is empty in manifest", header)
	}

	return value, nil
}

//manifestFilePath returns the manifest location for an exploded iflow folder
func manifestFilePath(artifactDir string) string {
	return filepath.Join(artifactDir, filepath.FromSlash(ManifestPath))
}

//ReadManifestHeader reads a single header from an exploded iflow folder
func ReadManifestHeader(artifactDir string, header string) (string, error) {
	data, err := os.ReadFile(manifestFilePath(artifactDir))
	if err != nil {
		return "", err
	}
	return readHeader(data, header)
}

//ReadBundleVersion returns the Bundle-Version of an exploded iflow folder
func ReadBundleVersion(artifactDir string) (string, error) {
	return ReadManifestHeader(artifactDir, HeaderBundleVersion)
}

//ReadBundleSymbolicName returns the Bundle-SymbolicName without OSGi directives
//such as "; singleton:=true"
func ReadBundleSymbolicName(artifactDir string) (string, error) {
	value, err := ReadManifestHeader(artifactDir, HeaderBundleSymbolicName)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.Split(value, ";")[0]), nil
}

//ReadBundleName returns the human readable Bundle-Name
func ReadBundleName(artifactDir string) (string, error) {
	return ReadManifestHeader(artifactDir, HeaderBundleName)
}

//setHeaders replaces the named headers in raw manifest bytes. Every other byte,
//including the 72 column wrapped Import-Package block and the original line
//terminators, is preserved. Continuation lines of a replaced header are dropped.
func setHeaders(data []byte, updates map[string]string) ([]byte, error) {

	lines := splitManifestLines(data)

	//Sorted, so that a manifest is rewritten identically on every run
	headers := make([]string, 0, len(updates))
	for header := range updates {
		headers = append(headers, header)
	}
	sort.Strings(headers)

	for _, header := range headers {
		index, continuations := headerIndex(lines, header)
		if index < 0 {
			return nil, fmt.Errorf("header %s not found in manifest", header)
		}

		updated := make([]manifestLine, 0, len(lines))
		updated = append(updated, lines[:index]...)
		updated = append(updated, manifestLine{
			Content:    header + ": " + updates[header],
			Terminator: lines[index].Terminator,
		})
		updated = append(updated, lines[index+continuations+1:]...)

		lines = updated
	}

	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.Content)
		builder.WriteString(line.Terminator)
	}

	return []byte(builder.String()), nil
}

//RewriteManifestHeaders returns manifest bytes with the named headers replaced.
//Used to rename an artifact inside a packed archive without ever touching the
//working copy in the repository.
func RewriteManifestHeaders(data []byte, updates map[string]string) ([]byte, error) {
	return setHeaders(data, updates)
}

//ReplaceSymbolicName sets the name part of a Bundle-SymbolicName, keeping any
//OSGi directives intact. Unlike ApplySuffixToSymbolicName it is idempotent,
//which matters for an archive that already carries a suffixed name.
func ReplaceSymbolicName(value string, name string) string {

	directives := ""
	if index := strings.Index(value, ";"); index >= 0 {
		directives = value[index:]
	}

	return name + directives
}

//ApplySuffixToSymbolicName appends an environment suffix to the name part of a
//Bundle-SymbolicName, keeping any OSGi directives intact:
//"Order_API; singleton:=true" with suffix "QA" becomes
//"Order_APIQA; singleton:=true"
func ApplySuffixToSymbolicName(value string, suffix string) string {
	if suffix == "" {
		return value
	}

	name := value
	directives := ""

	if index := strings.Index(value, ";"); index >= 0 {
		name = value[:index]
		directives = value[index:]
	}

	return strings.TrimRight(name, " \t") + suffix + directives
}

//SetBundleVersion rewrites only the Bundle-Version line of the manifest of an
//exploded iflow folder. The file is replaced atomically.
func SetBundleVersion(artifactDir string, version string) error {
	path := manifestFilePath(artifactDir)

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated, err := setHeaders(data, map[string]string{HeaderBundleVersion: version})
	if err != nil {
		return fmt.Errorf("%w in %s", err, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	return WriteFileAtomic(path, updated, info.Mode().Perm())
}

//ReadManifestBytes returns the raw manifest of an exploded iflow folder
func ReadManifestBytes(artifactDir string) ([]byte, error) {
	return os.ReadFile(manifestFilePath(artifactDir))
}
