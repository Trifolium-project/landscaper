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
