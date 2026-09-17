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
	"strconv"
	"strings"
)

//Bump levels accepted by Bump and by the --bump flag
const (
	BumpPatch = "patch"
	BumpMinor = "minor"
	BumpMajor = "major"
)

//Version is an OSGi bundle version. The optional fourth component (qualifier)
//is preserved in Raw but ignored for comparison and bumping.
type Version struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

//String renders the version in the major.minor.patch form used in MANIFEST.MF
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

//ParseVersion accepts "1", "1.0", "1.0.3" and "1.0.3.qualifier"
func ParseVersion(value string) (Version, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return Version{}, fmt.Errorf("version is empty")
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) > 4 {
		return Version{}, fmt.Errorf("unable to parse version %q: too many components", value)
	}

	version := Version{Raw: trimmed}
	targets := []*int{&version.Major, &version.Minor, &version.Patch}

	for index, target := range targets {
		if index >= len(parts) {
			break
		}
		number, err := strconv.Atoi(parts[index])
		if err != nil {
			return Version{}, fmt.Errorf("unable to parse version %q: component %q is not a number", value, parts[index])
		}
		if number < 0 {
			return Version{}, fmt.Errorf("unable to parse version %q: negative component", value)
		}
		*target = number
	}

	return version, nil
}

//Compare returns -1 if a < b, 0 if equal, 1 if a > b. The qualifier is ignored.
func Compare(a Version, b Version) int {
	pairs := [][2]int{
		{a.Major, b.Major},
		{a.Minor, b.Minor},
		{a.Patch, b.Patch},
	}

	for _, pair := range pairs {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}

	return 0
}

//Bump increases the requested component and zeroes the lower ones
func Bump(version Version, level string) (Version, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case BumpPatch:
		version.Patch++
	case BumpMinor:
		version.Minor++
		version.Patch = 0
	case BumpMajor:
		version.Major++
		version.Minor = 0
		version.Patch = 0
	default:
		return Version{}, fmt.Errorf("unknown bump level %q, expected one of %s, %s, %s", level, BumpPatch, BumpMinor, BumpMajor)
	}

	version.Raw = version.String()
	return version, nil
}
