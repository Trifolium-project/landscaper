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
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/iflow"
	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/spf13/cobra"
)

var (
	uploadTargetEnv        *string
	uploadDeploy           *bool
	uploadSkipVersionCheck *bool
	uploadBump             *string
	uploadSetVersion       *string
	uploadOutputDir        *string
	uploadWait             *bool
	uploadTimeout          *time.Duration
	uploadInterval         *time.Duration
)

//uploadRow is a single line of the report printed at the end of the run
type uploadRow struct {
	ArtifactId    string
	Source        string
	PackageId     string
	TenantVersion string
	UploadVersion string
	Action        string
	Deployed      bool
	//Set only when --wait was given alongside --deploy
	Status *deployStatus
}

// uploadCmd represents the artifact upload command
var artifactUploadCmd = &cobra.Command{
	Use:   "upload <artifact-folder-or-zip>...",
	Short: "Upload artifacts from the local repository to an environment",
	Long: `Upload integration flows from the local repository to a target environment.

Each argument is either a folder holding an exploded integration flow, which is
packed first with the same logic as "artifact pack", or a ready zip archive,
which is uploaded as it is.

The target package is taken from the landscape configuration and, together with
the artifact id and name, receives the suffix of the target environment. The
package is created when it does not exist yet. Configuration parameters
declared for the target environment are applied after the upload.

Use --deploy to deploy the artifacts once they are uploaded.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Uploading %d artifact(s) to %s...\n", len(args), *uploadTargetEnv)
		artifactUpload(args)
	},
}

func init() {
	artifactCmd.AddCommand(artifactUploadCmd)

	uploadTargetEnv = artifactUploadCmd.Flags().String("target-env", "", "Target environment")
	uploadDeploy = artifactUploadCmd.Flags().BoolP("deploy", "d", false, "Indicate whether necessary to deploy the artifacts after upload")
	uploadSkipVersionCheck = artifactUploadCmd.Flags().Bool("skip-version-check", false, "Do not compare the local version with the version in the tenant")
	uploadBump = artifactUploadCmd.Flags().String("bump", "", "Increase the version without asking: patch, minor or major")
	uploadSetVersion = artifactUploadCmd.Flags().String("set-version", "", "Use this exact version instead of asking")
	uploadOutputDir = artifactUploadCmd.Flags().String("output", defaultArtifactOutputDir, "Folder for the archives generated from folders")
	uploadWait = artifactUploadCmd.Flags().Bool("wait", false, "With --deploy, wait until each deployment is finished and report the result")
	uploadTimeout = artifactUploadCmd.Flags().Duration("timeout", defaultDeployTimeout, "How long to wait for a deployment")
	uploadInterval = artifactUploadCmd.Flags().Duration("interval", defaultDeployInterval, "How often to ask the tenant while waiting")

	artifactUploadCmd.MarkFlagRequired("target-env")
}

func artifactUpload(paths []string) {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	targetEnvironment, err := globalLandscape.GetEnvironment(*uploadTargetEnv)
	if err != nil {
		log.Fatalln(err)
	}

	warnIgnoredVersionFlags(
		targetEnvironment.Id == globalLandscape.OriginalEnvironment.Id,
		targetEnvironment, *uploadBump, *uploadSetVersion,
	)

	waiting := uploadWait != nil && *uploadWait && *uploadDeploy

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	if waiting {
		fmt.Fprintf(writer, "#\tArtefactId\tSource\tPackage\tVersion in %s\tUploaded Version\tAction\tDeployed\tRuntime Status\tError\n", targetEnvironment.Id)
	} else {
		fmt.Fprintf(writer, "#\tArtefactId\tSource\tPackage\tVersion in %s\tUploaded Version\tAction\tDeployed\n", targetEnvironment.Id)
	}

	rows := make([]*uploadRow, 0, len(paths))

	flush := func() {
		for index, row := range rows {
			if waiting {
				fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
					index+1, row.ArtifactId, row.Source, row.PackageId,
					row.TenantVersion, row.UploadVersion, row.Action, row.Deployed,
					row.Status.Summary(), deployStatusError(row.Status))
			} else {
				fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%t\n",
					index+1, row.ArtifactId, row.Source, row.PackageId,
					row.TenantVersion, row.UploadVersion, row.Action, row.Deployed)
			}

			//The audit log carries the same status vocabulary as the table
			auditItem(map[string]interface{}{
				"operation":      "upload",
				"artifact":       row.ArtifactId,
				"package":        row.PackageId,
				"source":         row.Source,
				"tenant_version": row.TenantVersion,
				"version":        row.UploadVersion,
				"status":         row.Action,
				"deployed":       row.Deployed,
			})
		}
		writer.Flush()
	}

	for _, path := range paths {
		row, err := uploadArtifact(filepath.Clean(path), targetEnvironment)
		if err != nil {
			flush()
			log.Fatalln(err)
		}
		rows = append(rows, row)

		//A failed deployment stops the run, like any other failure, but the
		//rows already produced are still reported
		if row.Status != nil && row.Status.ExitCode() != exitDeployed {
			flush()
			printDeployError(row.Status)
			log.Printf("Deployment of %s did not succeed: %s", row.ArtifactId, row.Status.Summary())
			exitWith(row.Status.ExitCode())
		}
	}

	flush()
}

//uploadArtifact packs when needed and pushes a single artifact to the tenant
func uploadArtifact(path string, targetEnvironment *landscape.Environment) (*uploadRow, error) {

	//Checked before anything else, so that a wrong path is reported as a wrong
	//path rather than as a missing landscape declaration
	if !iflow.IsZipPath(path) && !iflow.IsArtifactDir(path) {
		return nil, fmt.Errorf("%s is neither an integration flow folder nor a zip archive", path)
	}

	//An archive packed for the target environment already carries the suffix in
	//its file name, so it has to be stripped before the suffix is applied again
	baseArtifactId := trimTargetSuffix(artifactIdFromPath(path), targetEnvironment.Suffix)

	basePackageId, err := resolveBasePackageId(baseArtifactId)
	if err != nil {
		return nil, err
	}
	targetPackageId := basePackageId + targetEnvironment.Suffix
	targetArtifactId := baseArtifactId + targetEnvironment.Suffix

	client := targetEnvironment.System.Client

	//Outside the original environment the repository owns the version: it is
	//uploaded as it is and META-INF/MANIFEST.MF is never written back
	isOriginal := targetEnvironment.Id == globalLandscape.OriginalEnvironment.Id

	//Read once, up front: it decides both whether a version bump is required
	//and whether the artifact has to be created or updated. It is therefore
	//needed even when --skip-version-check is given.
	tenantVersion := readTenantArtifactVersion(targetEnvironment, targetPackageId, targetArtifactId)

	row := &uploadRow{
		ArtifactId:    targetArtifactId,
		PackageId:     targetPackageId,
		TenantVersion: tenantVersion,
	}

	var content []byte
	var bundleName string
	rewriteArchive := false

	switch {
	case iflow.IsZipPath(path):
		row.Source = "zip"

		content, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		version, err := iflow.ReadBundleVersionFromZip(content)
		if err != nil {
			return nil, err
		}
		row.UploadVersion = version

		bundleName, _ = iflow.ReadBundleNameFromZip(content)
		//The archive may already have been packed for this environment
		bundleName = trimNameSuffix(bundleName, targetEnvironment.Suffix)
		rewriteArchive = true

	case iflow.IsArtifactDir(path):
		row.Source = "folder"

		result, err := packArtifact(packOptions{
			SourceDir:          path,
			CheckEnvironment:   targetEnvironment,
			PackageId:          targetPackageId,
			TenantArtId:        targetArtifactId,
			TenantVersion:      tenantVersion,
			Suffix:             targetEnvironment.Suffix,
			AllowVersionUpdate: isOriginal,
			SkipCheck:          *uploadSkipVersionCheck,
			Bump:               *uploadBump,
			SetVersion:         *uploadSetVersion,
			OutputDir:          *uploadOutputDir,
		})
		if err != nil {
			return nil, err
		}

		content = result.Zip
		row.UploadVersion = result.PackedVersion

		bundleName, _ = iflow.ReadBundleName(path)

	default:
		return nil, fmt.Errorf("%s is neither an integration flow folder nor a zip archive", path)
	}

	if bundleName == "" {
		bundleName = baseArtifactId
	}

	//Computed once and used both for the OData metadata and, inside
	//packArtifact, for Bundle-Name in the archive, so the two cannot drift
	targetBundleName := strings.TrimSpace(bundleName + " " + targetEnvironment.Suffix)

	//A ready archive carries whatever identifiers it was packed with, so it is
	//rewritten to the ones this environment expects
	if rewriteArchive && targetEnvironment.Suffix != "" {
		content, err = rewriteArchiveForEnvironment(content, targetArtifactId, targetBundleName)
		if err != nil {
			return nil, err
		}
	}

	if err := ensureTargetPackage(targetEnvironment, targetPackageId, basePackageId); err != nil {
		return nil, err
	}

	artifactExists := tenantVersion != versionNotInTenant

	newArtifact := &cpiclient.IntegrationDesigntimeArtifact{
		Id:              targetArtifactId,
		PackageId:       targetPackageId,
		Name:            targetBundleName,
		Description:     "",
		ArtifactContent: base64.StdEncoding.EncodeToString(content),
	}

	if artifactExists {
		if tenantVersion == "Active" {
			log.Printf("Artifact %s is in Draft state in environment %s and is about to be overwritten",
				targetArtifactId, targetEnvironment.Id)
		}
		if err := client.UpdateIntegrationDesigntimeArtifact(newArtifact); err != nil {
			return nil, err
		}
		row.Action = "updated"
	} else {
		if err := client.UploadIntegrationDesigntimeArtifact(newArtifact); err != nil {
			return nil, err
		}
		row.Action = "created"
	}

	//The tenant is the authority on the resulting version, the local manifest
	//is only a request
	deployedVersion := readTenantArtifactVersion(targetEnvironment, targetPackageId, targetArtifactId)
	if deployedVersion != versionNotInTenant {
		row.UploadVersion = deployedVersion
	}

	if err := applyArtifactConfiguration(targetEnvironment, basePackageId, baseArtifactId, targetArtifactId, row.UploadVersion); err != nil {
		return nil, err
	}

	if *uploadDeploy {
		if err := client.DeployIntegrationDesigntimeArtifact(targetArtifactId, row.UploadVersion); err != nil {
			return nil, err
		}
		row.Deployed = true

		if uploadWait != nil && *uploadWait {
			//The runtime keeps reporting the previous version as STARTED for a
			//while after a redeploy, so the wait only accepts the version that
			//was just uploaded
			row.Status = waitForDeployment(client, targetArtifactId, row.UploadVersion,
				*uploadTimeout, *uploadInterval)
		}
	}

	return row, nil
}

//trimNameSuffix removes a trailing " <suffix>" from a display name, so that a
//name is not suffixed twice
func trimNameSuffix(name string, suffix string) string {
	if suffix == "" {
		return name
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), " "+suffix))
}

//trimTargetSuffix removes the environment suffix from an identifier that
//already carries it. The suffix is only stripped when what remains is an
//artifact the landscape declares, so an artifact whose name genuinely ends
//with the suffix is left alone.
func trimTargetSuffix(artifactId string, suffix string) string {

	if suffix == "" || artifactId == suffix || !strings.HasSuffix(artifactId, suffix) {
		return artifactId
	}

	trimmed := strings.TrimSuffix(artifactId, suffix)
	if trimmed == "" {
		return artifactId
	}

	if _, err := globalLandscape.FindPackageForArtifact(trimmed); err != nil {
		return artifactId
	}

	return trimmed
}

//rewriteArchiveForEnvironment sets Bundle-SymbolicName and Bundle-Name of an
//already packed archive to the identifiers it will carry in the target tenant.
//It is written as a replacement rather than as an append, so that an archive
//that was already packed for this environment is left as it is instead of
//being suffixed twice.
func rewriteArchiveForEnvironment(content []byte, artifactId string, bundleName string) ([]byte, error) {

	symbolicName, err := iflow.ReadManifestHeaderFromZip(content, iflow.HeaderBundleSymbolicName)
	if err != nil {
		return nil, err
	}

	updates := map[string]string{
		iflow.HeaderBundleSymbolicName: iflow.ReplaceSymbolicName(symbolicName, artifactId),
		iflow.HeaderBundleName:         bundleName,
	}

	return iflow.RewriteZipManifest(content, updates)
}

//resolveBasePackageId returns the package id without any environment suffix.
//The global --pkg flag wins when it is given.
func resolveBasePackageId(artifactId string) (string, error) {
	if *pkg != "" {
		return *pkg, nil
	}

	return globalLandscape.FindPackageForArtifact(artifactId)
}

//ensureTargetPackage creates the target package when it does not exist yet
func ensureTargetPackage(targetEnvironment *landscape.Environment, targetPackageId string, basePackageId string) error {

	existing, err := targetEnvironment.System.Client.ReadIntegrationPackage(targetPackageId)
	if err != nil {
		log.Printf("Package %s is not available in environment %s, it will be created", targetPackageId, targetEnvironment.Id)
	}
	if existing != nil {
		return nil
	}

	newPackage := &cpiclient.IntegrationPackage{
		Id:          targetPackageId,
		Name:        strings.TrimSpace(targetEnvironment.Suffix + " " + basePackageId),
		Description: "Created by landscaper from the local repository",
		ShortText:   basePackageId + " (environment - '" + targetEnvironment.Id + "')",
		Vendor:      "",
		Version:     "1.0.0",
		Keywords:    "",
	}

	return targetEnvironment.System.Client.CreateIntegrationPackage(newPackage)
}

//applyArtifactConfiguration pushes the parameters declared in the landscape
//configuration for the target environment
func applyArtifactConfiguration(targetEnvironment *landscape.Environment, basePackageId string,
	baseArtifactId string, targetArtifactId string, version string) error {

	parameters, err := globalLandscape.GetArtifactConfiguration(*uploadTargetEnv, basePackageId, baseArtifactId)
	if err != nil {
		return err
	}

	for _, parameter := range parameters {
		configuration := &cpiclient.Configuration{
			ParameterKey:   parameter.Key,
			ParameterValue: parameter.Value,
			DataType:       parameter.Type,
		}

		err = targetEnvironment.System.Client.UpdateIntegrationDesigntimeArtifactConfiguration(
			targetArtifactId, version, configuration,
		)
		if err != nil {
			return err
		}
	}

	return nil
}
