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
package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/iflow"
	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/Trifolium-project/landscaper/packages/util"
	"github.com/spf13/cobra"
)

//Default folder for downloaded artifacts
const defaultDownloadOutputDir = "artifacts"

//Version of an artifact, that the tenant holds as a draft
const draftVersion = "Active"

var (
	downloadPackages  *[]string
	downloadArtifacts *[]string
	downloadAll       *bool
	downloadOutputDir *string
	downloadForce     *bool
)

//downloadTarget is one physical package to read, optionally narrowed down to
//single artifacts of that package
type downloadTarget struct {
	PackageId string
	//Physical artifact ids to keep. Empty means every artifact of the package
	ArtifactIds []string
}

//downloadRow is a single line of the report printed at the end of the run
type downloadRow struct {
	ArtifactId string
	PackageId  string
	Version    string
	Status     string
	Path       string
	Failed     bool
}

// downloadCmd represents the artifact download command
var artifactDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download artifacts from an environment into the local repository",
	Long: `Download integration flows of an environment and extract them into a local
folder, one folder per package and one folder per artifact inside it:

    <output>/<package id>/<artifact id>/META-INF/MANIFEST.MF

The latest version of every artifact is taken. Ids are written exactly as the
tenant has them, so downloading from an environment with a suffix produces
suffixed folder names.

Pass either --packages, or --artifacts, or --download-all. The ids of
--packages and --artifacts are the base ids of the landscape configuration,
without the environment suffix, which is appended by the command.

Only integration flows are downloaded. Value mappings, message mappings and
script collections live in other entity sets and are not covered. Packages
delivered by SAP are skipped by --download-all, because their content cannot be
downloaded.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactDownload()
	},
}

func init() {
	artifactCmd.AddCommand(artifactDownloadCmd)

	downloadPackages = artifactDownloadCmd.Flags().StringSlice("packages", []string{}, "Comma separated list of package ids to download, without environment suffix")
	downloadArtifacts = artifactDownloadCmd.Flags().StringSlice("artifacts", []string{}, "Comma separated list of artifact ids to download, without environment suffix")
	downloadAll = artifactDownloadCmd.Flags().Bool("download-all", false, "Download every package of the environment")
	downloadOutputDir = artifactDownloadCmd.Flags().String("output", defaultDownloadOutputDir, "Folder for the downloaded artifacts")
	downloadForce = artifactDownloadCmd.Flags().Bool("force", false, "Replace artifact folders, that already exist. The default output folder is gitignored, so local changes cannot be restored with git")
}

func artifactDownload() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	if err := validateDownloadFlags(); err != nil {
		log.Fatalln(err)
	}

	environment_, err := globalLandscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)
	}

	outputDir := *downloadOutputDir
	fmt.Printf("Downloading artifacts from %s into %s...\n", environment_.Id, outputDir)

	targets, err := resolveDownloadTargets(environment_)
	if err != nil {
		log.Fatalln(err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "#\tArtefactId\tPackage\tVersion in %s\tStatus\tPath\n", environment_.Id)

	rows := []*downloadRow{}

	flush := func() {
		for index, row := range rows {
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\n",
				index+1, row.ArtifactId, row.PackageId, row.Version, row.Status, row.Path)

			//The audit log carries the same status vocabulary as the table
			auditItem(map[string]interface{}{
				"operation": "download",
				"artifact":  row.ArtifactId,
				"package":   row.PackageId,
				"version":   row.Version,
				"status":    row.Status,
				"path":      row.Path,
				"failed":    row.Failed,
			})
		}
		writer.Flush()
	}

	for _, target := range targets {

		artifacts, err := environment_.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
		if err != nil {
			rows = append(rows, &downloadRow{
				ArtifactId: "-",
				PackageId:  target.PackageId,
				Version:    "-",
				Status:     fmt.Sprintf("failed: package cannot be read: %s", singleLine(err.Error())),
				Path:       "-",
				Failed:     true,
			})
			continue
		}

		rows = append(rows, downloadPackageArtifacts(environment_, target, artifacts, outputDir)...)
	}

	flush()

	failed := 0
	for _, row := range rows {
		if row.Failed {
			failed++
		}
	}

	if failed > 0 {
		log.Fatalf("%d of %d artifacts could not be downloaded", failed, len(rows))
	}
}

//validateDownloadFlags enforces that exactly one way of selecting artifacts is
//used. The global --pkg and --artifact are rejected rather than ignored,
//because root.go has already appended the environment suffix to them.
func validateDownloadFlags() error {

	selectors := 0
	if len(*downloadPackages) > 0 {
		selectors++
	}
	if len(*downloadArtifacts) > 0 {
		selectors++
	}
	if *downloadAll {
		selectors++
	}

	if selectors == 0 {
		return fmt.Errorf("Nothing to download, pass --packages, --artifacts or --download-all")
	}
	if selectors > 1 {
		return fmt.Errorf("Pass only one of --packages, --artifacts and --download-all")
	}

	if *pkg != "" || *artifact != "" {
		return fmt.Errorf("Use --packages and --artifacts instead of the global --pkg and --artifact, which already carry the environment suffix")
	}

	return nil
}

//resolveDownloadTargets turns the selection flags into the physical package ids
//of the environment, together with the artifacts to keep of each package
func resolveDownloadTargets(environment *landscape.Environment) ([]downloadTarget, error) {

	//Order of the targets follows the order of the flag values, so that the
	//report is predictable
	order := []string{}
	targets := map[string]*downloadTarget{}

	appendTarget := func(packageId string, artifactId string) error {

		if err := validatePathSegment("Package", packageId); err != nil {
			return err
		}
		if artifactId != "" {
			if err := validatePathSegment("Artifact", artifactId); err != nil {
				return err
			}
		}

		target := targets[packageId]
		if target == nil {
			target = &downloadTarget{PackageId: packageId}
			targets[packageId] = target
			order = append(order, packageId)
		}

		if artifactId != "" && !util.Contains(target.ArtifactIds, artifactId) {
			target.ArtifactIds = append(target.ArtifactIds, artifactId)
		}

		return nil
	}

	switch {

	case len(*downloadPackages) > 0:
		for _, basePackageId := range *downloadPackages {
			if err := appendTarget(basePackageId+environment.Suffix, ""); err != nil {
				return nil, err
			}
		}

	case len(*downloadArtifacts) > 0:
		for _, baseArtifactId := range *downloadArtifacts {
			basePackageId, err := globalLandscape.FindPackageForArtifact(baseArtifactId)
			if err != nil {
				return nil, err
			}
			if err := appendTarget(basePackageId+environment.Suffix, baseArtifactId+environment.Suffix); err != nil {
				return nil, err
			}
		}

	case *downloadAll:
		packages, warnings, err := globalLandscape.PackageIdsForEnvironment(environment)
		if err != nil {
			return nil, err
		}
		for _, warning := range warnings {
			log.Println(warning)
		}
		for _, packageId := range packages {
			if err := appendTarget(packageId, ""); err != nil {
				return nil, err
			}
		}
	}

	ordered := make([]downloadTarget, 0, len(order))
	for _, packageId := range order {
		ordered = append(ordered, *targets[packageId])
	}

	return ordered, nil
}

//downloadPackageArtifacts downloads the requested artifacts of one package and
//reports one row per artifact. A failing artifact does not stop the others.
func downloadPackageArtifacts(environment *landscape.Environment, target downloadTarget,
	artifacts []*cpiclient.IntegrationDesigntimeArtifact, outputDir string) []*downloadRow {

	rows := []*downloadRow{}
	found := map[string]bool{}

	for _, integrationArtifact := range artifacts {

		if len(target.ArtifactIds) > 0 && !util.Contains(target.ArtifactIds, integrationArtifact.Id) {
			continue
		}
		found[integrationArtifact.Id] = true

		row, err := downloadArtifact(environment, target.PackageId, integrationArtifact, outputDir)
		if err != nil {
			row = &downloadRow{
				ArtifactId: integrationArtifact.Id,
				PackageId:  target.PackageId,
				Version:    integrationArtifact.Version,
				Status:     fmt.Sprintf("failed: %s", singleLine(err.Error())),
				Path:       "-",
				Failed:     true,
			}
		}
		rows = append(rows, row)
	}

	//An explicitly requested artifact, that the package does not hold, is an
	//error of the caller and has to be visible
	for _, artifactId := range target.ArtifactIds {
		if found[artifactId] {
			continue
		}
		rows = append(rows, &downloadRow{
			ArtifactId: artifactId,
			PackageId:  target.PackageId,
			Version:    "-",
			Status:     "failed: artifact is not in the package",
			Path:       "-",
			Failed:     true,
		})
	}

	return rows
}

//downloadArtifact fetches one artifact and extracts it into
//<outputDir>/<packageId>/<artifactId>
func downloadArtifact(environment *landscape.Environment, packageId string,
	integrationArtifact *cpiclient.IntegrationDesigntimeArtifact, outputDir string) (*downloadRow, error) {

	if err := validatePathSegment("Artifact", integrationArtifact.Id); err != nil {
		return nil, err
	}

	//An empty version is not expected, but "Active" is what the tenant answers
	//for a draft and is also the valid argument of the download call
	version := integrationArtifact.Version
	if version == "" {
		version = draftVersion
	}

	destination := filepath.Join(outputDir, packageId, integrationArtifact.Id)

	row := &downloadRow{
		ArtifactId: integrationArtifact.Id,
		PackageId:  packageId,
		Version:    version,
		Path:       destination,
	}

	//Checked before the download, so that a skipped artifact costs no request
	if !*downloadForce && folderExists(destination) {
		row.Status = "skipped, folder exists (use --force)"
		return row, nil
	}

	content, err := environment.System.Client.DownloadIntegrationDesigntimeArtifactContent(integrationArtifact.Id, version)
	if err != nil {
		return nil, err
	}

	//doRequest only inspects the leading digit of the status code, so an error
	//page answered with 200 would be extracted onto disk without this check
	if _, err := iflow.ReadManifestHeaderFromZip(content, iflow.HeaderBundleVersion); err != nil {
		return nil, fmt.Errorf("the tenant did not return an integration flow archive for %s: %s", integrationArtifact.Id, err)
	}

	if err := iflow.UnzipToDir(content, destination); err != nil {
		return nil, err
	}

	row.Status = "downloaded"
	if version == draftVersion {
		row.Status = "downloaded (draft)"
	}

	return row, nil
}

//validatePathSegment refuses an id, that would not stay inside the output
//folder when it is used as a folder name
func validatePathSegment(kind string, id string) error {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return fmt.Errorf("%s id %q cannot be used as a folder name", kind, id)
	}
	return nil
}

//folderExists reports whether the path is an existing directory
func folderExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

//singleLine reduces an error to one line, so that it does not break the table.
//An OData error body is JSON and arrives with line breaks.
func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
