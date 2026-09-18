package iflow

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

//writeIflowProject builds a project resembling artifacts/Order_API_TEST_HARNESS
func writeIflowProject(t *testing.T) string {
	t.Helper()

	root := writeArtifactDir(t, sampleManifest)

	files := map[string]string{
		".project":      "<projectDescription/>",
		"metainfo.prop": "description=",
		"src/main/resources/parameters.prop":                            "urlPath=/erp/order",
		"src/main/resources/script/script1.groovy":                      "// groovy",
		"src/main/resources/scenarioflows/integrationflow/Sample.iflw":  "<bpmn/>",
		".DS_Store":          "junk",
		"src/main/.DS_Store": "junk",
	}

	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("unable to create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("unable to write %s: %v", path, err)
		}
	}

	return root
}

func archiveNames(t *testing.T, data []byte) []string {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("unable to read archive: %v", err)
	}

	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	sort.Strings(names)

	return names
}

func TestZipDirLaysManifestAtTheRoot(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := archiveNames(t, data)

	want := []string{
		".project",
		"META-INF/MANIFEST.MF",
		"metainfo.prop",
		"src/main/resources/parameters.prop",
		"src/main/resources/scenarioflows/integrationflow/Sample.iflw",
		"src/main/resources/script/script1.groovy",
	}
	sort.Strings(want)

	if len(names) != len(want) {
		t.Fatalf("archive contains %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("archive contains %v, want %v", names, want)
		}
	}
}

func TestZipDirExcludesJunkFiles(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range archiveNames(t, data) {
		if filepath.Base(name) == ".DS_Store" {
			t.Errorf("archive contains %s, it should have been excluded", name)
		}
	}
}

func TestZipDirContentRoundTrip(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("unable to read archive: %v", err)
	}

	for _, file := range reader.File {
		packed, err := file.Open()
		if err != nil {
			t.Fatalf("unable to open %s: %v", file.Name, err)
		}
		got, err := io.ReadAll(packed)
		packed.Close()
		if err != nil {
			t.Fatalf("unable to read %s: %v", file.Name, err)
		}

		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Name)))
		if err != nil {
			t.Fatalf("unable to read source of %s: %v", file.Name, err)
		}

		if !bytes.Equal(got, want) {
			t.Errorf("%s content differs after packing", file.Name)
		}
	}
}

func TestZipDirIsDeterministic(t *testing.T) {
	root := writeIflowProject(t)

	first, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Error("ZipDir produced different bytes for the same input")
	}
}

func TestZipDirRejectsFolderWithoutManifest(t *testing.T) {
	root := t.TempDir()

	if _, err := ZipDir(root); err == nil {
		t.Fatal("expected an error for a folder without META-INF/MANIFEST.MF")
	}
}

func TestReadBundleVersionFromZip(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	version, err := ReadBundleVersionFromZip(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1.0.3" {
		t.Errorf("ReadBundleVersionFromZip = %q, want %q", version, "1.0.3")
	}

	name, err := ReadBundleNameFromZip(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Order API for Test Harness" {
		t.Errorf("ReadBundleNameFromZip = %q, want %q", name, "Order API for Test Harness")
	}
}

func TestReadBundleVersionFromZipWithoutManifest(t *testing.T) {
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	writer, err := archive.Create("readme.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writer.Write([]byte("nothing here"))
	archive.Close()

	if _, err := ReadBundleVersionFromZip(buffer.Bytes()); err == nil {
		t.Fatal("expected an error for an archive without a manifest")
	}
}

func TestIsZipPathAndIsArtifactDir(t *testing.T) {
	root := writeIflowProject(t)

	if !IsArtifactDir(root) {
		t.Error("IsArtifactDir should accept an exploded iflow folder")
	}
	if IsZipPath(root) {
		t.Error("IsZipPath should reject a folder")
	}

	archivePath := filepath.Join(t.TempDir(), "Order_API_TEST_HARNESS.zip")
	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteFileAtomic(archivePath, data, 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !IsZipPath(archivePath) {
		t.Error("IsZipPath should accept an existing zip file")
	}
	if IsArtifactDir(archivePath) {
		t.Error("IsArtifactDir should reject a zip file")
	}
	if IsZipPath(filepath.Join(t.TempDir(), "missing.zip")) {
		t.Error("IsZipPath should reject a path that does not exist")
	}
}

func TestWriteFileAtomicCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build", "nested", "artifact.zip")

	if err := WriteFileAtomic(path, []byte("payload"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unable to read written file: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("content = %q, want %q", string(data), "payload")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("unable to list directory: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, temporary file was not cleaned up", len(entries))
	}
}

func TestZipDirWithOverridesReplacesOnlyTheNamedEntry(t *testing.T) {
	root := writeIflowProject(t)

	replacement := []byte("Manifest-Version: 1.0\r\nBundle-Version: 9.9.9\r\n\r\n")
	data, err := ZipDirWithOverrides(root, map[string][]byte{ManifestPath: replacement})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	version, err := ReadBundleVersionFromZip(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "9.9.9" {
		t.Errorf("Bundle-Version = %q, want %q", version, "9.9.9")
	}

	//Everything else still comes from disk
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("unable to read archive: %v", err)
	}
	for _, file := range reader.File {
		if file.Name == ManifestPath {
			continue
		}
		packed, _ := file.Open()
		got, _ := io.ReadAll(packed)
		packed.Close()

		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Name)))
		if err != nil {
			t.Fatalf("unable to read source: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s was altered by the override", file.Name)
		}
	}

	//The file on disk must be untouched
	onDisk, err := ReadBundleVersion(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if onDisk != "1.0.3" {
		t.Errorf("the override leaked to disk, Bundle-Version = %q", onDisk)
	}
}

func TestRewriteZipManifest(t *testing.T) {
	root := writeIflowProject(t)

	original, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rewritten, err := RewriteZipManifest(original, map[string]string{
		HeaderBundleSymbolicName: "Order_API_TEST_HARNESSQA; singleton:=true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	name, err := ReadManifestHeaderFromZip(rewritten, HeaderBundleSymbolicName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Order_API_TEST_HARNESSQA; singleton:=true" {
		t.Errorf("Bundle-SymbolicName = %q", name)
	}

	//Same entries, same content apart from the manifest
	before := archiveNames(t, original)
	after := archiveNames(t, rewritten)
	if len(before) != len(after) {
		t.Fatalf("entry count changed: %v vs %v", before, after)
	}
	for index := range before {
		if before[index] != after[index] {
			t.Fatalf("entries changed: %v vs %v", before, after)
		}
	}

	if version, _ := ReadBundleVersionFromZip(rewritten); version != "1.0.3" {
		t.Errorf("Bundle-Version = %q, want it unchanged", version)
	}
}

func TestRewriteZipManifestWithoutManifest(t *testing.T) {
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	writer, _ := archive.Create("readme.txt")
	writer.Write([]byte("nothing"))
	archive.Close()

	_, err := RewriteZipManifest(buffer.Bytes(), map[string]string{HeaderBundleName: "x"})
	if err == nil {
		t.Fatal("expected an error for an archive without a manifest")
	}
}

//zipWithEntries builds an archive of the given names verbatim, so that entries,
//that ZipDir would never produce, can be tested
func zipWithEntries(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	names := []string{}
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)

	for _, name := range names {
		writer, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch})
		if err != nil {
			t.Fatalf("unable to add %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(entries[name])); err != nil {
			t.Fatalf("unable to write %s: %v", name, err)
		}
	}

	if err := archive.Close(); err != nil {
		t.Fatalf("unable to close archive: %v", err)
	}

	return buffer.Bytes()
}

func TestUnzipToDirRoundTripsZipDir(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "Order_API_TEST_HARNESS")
	if err := UnzipToDir(data, destination); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !IsArtifactDir(destination) {
		t.Fatalf("%s is not an integration flow folder after extraction", destination)
	}

	version, err := ReadBundleVersion(destination)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1.0.3" {
		t.Errorf("Bundle-Version = %q, want %q", version, "1.0.3")
	}

	//Every entry of the archive has to be on disk with the same content
	for _, name := range archiveNames(t, data) {
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("unable to read extracted %s: %v", name, err)
		}
		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("unable to read source of %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s content differs after extraction", name)
		}
	}
}

//os.MkdirTemp creates the staging folder with 0700, so without an explicit
//chmod the downloaded artifact ends up less readable than everything else
func TestUnzipToDirCreatesReadableFolders(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "artifact")
	if err := UnzipToDir(data, destination); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("folder mode = %v, want %v", info.Mode().Perm(), os.FileMode(0755))
	}

	file, err := os.Stat(filepath.Join(destination, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if file.Mode().Perm() != 0644 {
		t.Errorf("file mode = %v, want %v", file.Mode().Perm(), os.FileMode(0644))
	}
}

func TestUnzipToDirReplacesExistingFolder(t *testing.T) {
	root := writeIflowProject(t)

	data, err := ZipDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "artifact")
	stale := filepath.Join(destination, "stale.txt")
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := UnzipToDir(data, destination); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a file of the previous content survived the extraction")
	}
	if !IsArtifactDir(destination) {
		t.Error("the folder does not hold the new content")
	}
}

//The extraction happens in a staging folder, so a broken archive must leave the
//folder, that is already there, untouched
func TestUnzipToDirKeepsExistingFolderOnFailure(t *testing.T) {
	data := zipWithEntries(t, map[string]string{
		"META-INF/MANIFEST.MF": sampleManifest,
		"../escape.txt":        "owned",
	})

	parent := t.TempDir()
	destination := filepath.Join(parent, "artifact")
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "keep.txt"), []byte("mine"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := UnzipToDir(data, destination); err == nil {
		t.Fatal("expected an error for an entry pointing outside the target folder")
	}

	if _, err := os.Stat(filepath.Join(destination, "keep.txt")); err != nil {
		t.Errorf("the existing folder was destroyed by the failed extraction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !os.IsNotExist(err) {
		t.Error("an entry escaped the target folder")
	}
}

func TestUnzipToDirRejectsZipSlip(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{"parent traversal", "../escape.txt"},
		{"nested traversal", "src/../../escape.txt"},
		{"absolute path", "/etc/escape.txt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := zipWithEntries(t, map[string]string{test.entry: "owned"})

			parent := t.TempDir()
			if err := UnzipToDir(data, filepath.Join(parent, "artifact")); err == nil {
				t.Fatalf("expected an error for entry %q", test.entry)
			}

			if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !os.IsNotExist(err) {
				t.Errorf("entry %q escaped the target folder", test.entry)
			}
		})
	}
}

func TestUnzipToDirKeepsEmptyDirectories(t *testing.T) {
	data := zipWithEntries(t, map[string]string{
		"META-INF/MANIFEST.MF":     sampleManifest,
		"src/main/resources/":      "",
		"src/main/resources/x.prop": "urlPath=/erp/order",
	})

	destination := filepath.Join(t.TempDir(), "artifact")
	if err := UnzipToDir(data, destination); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(filepath.Join(destination, "src", "main", "resources"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.IsDir() {
		t.Error("src/main/resources is not a directory")
	}
}

func TestUnzipToDirRejectsEmptyArchive(t *testing.T) {
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	archive.Close()

	if err := UnzipToDir(buffer.Bytes(), filepath.Join(t.TempDir(), "artifact")); err == nil {
		t.Fatal("expected an error for an empty archive")
	}
}
