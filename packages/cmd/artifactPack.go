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
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/iflow"
	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//Default folder for generated archives
const defaultArtifactOutputDir = "build"

//Shown instead of a version number when the artifact is absent from the tenant
const versionNotInTenant = "-"

var (
	packSkipVersionCheck *bool
	packBump             *string
	packSetVersion       *string
	packOutputDir        *string
	packTargetEnv        *string
)

// packCmd represents the artifact pack command
var artifactPackCmd = &cobra.Command{
	Use:   "pack <artifact-folder>...",
	Short: "Pack integration flow folders into deployable zip archives",
	Long: `Pack integration flow folders from the local repository into zip archives.

The archive is named after the folder and contains every file inside it, with
META-INF/MANIFEST.MF at the archive root.

Without --target-env the artifact is packed for the original environment. The
Bundle-Version from META-INF/MANIFEST.MF is compared with the version currently
present there, and when the local version is not higher, a new version is
requested interactively, written back to META-INF/MANIFEST.MF and then packed.
For non interactive runs use --bump, --set-version or --skip-version-check.

With --target-env the artifact is packed for that environment: the version is
taken from the repository as it is, META-INF/MANIFEST.MF is never written, and
Bundle-SymbolicName and Bundle-Name receive the environment suffix inside the
archive only.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		artifactPack(args)
	},
}

func init() {
	artifactCmd.AddCommand(artifactPackCmd)

	packSkipVersionCheck = artifactPackCmd.Flags().Bool("skip-version-check", false, "Do not compare the local version with the version in the tenant")
	packBump = artifactPackCmd.Flags().String("bump", "", "Increase the version without asking: patch, minor or major")
	packSetVersion = artifactPackCmd.Flags().String("set-version", "", "Use this exact version instead of asking")
	packOutputDir = artifactPackCmd.Flags().String("output", defaultArtifactOutputDir, "Folder for the generated archives")
	packTargetEnv = artifactPackCmd.Flags().String("target-env", "", "Pack for this environment instead of the original one")
}

//packOptions describes a single pack request. It is also used by
//"artifact upload", which packs folders before sending them to a tenant.
type packOptions struct {
	SourceDir string
	//Tenant to compare the version against. Nil means no check at all.
	CheckEnvironment *landscape.Environment
	//Package and artifact identifiers as they appear in CheckEnvironment,
	//including the environment suffix
	PackageId   string
	TenantArtId string
	//Version already read from CheckEnvironment, or versionNotInTenant.
	//The caller reads it, because "artifact upload" needs it as well to decide
	//between creating and updating the artifact.
	TenantVersion string
	//Suffix applied to Bundle-SymbolicName and Bundle-Name inside the archive
	//only. Empty means the archive keeps the identifiers of the repository.
	//The tenant derives the symbolic name from the artifact id, so an archive
	//uploaded under a suffixed id must carry the suffixed symbolic name too.
	Suffix string
	//True only when packing for the original environment. When false the
	//version is taken from the repository as it is and META-INF/MANIFEST.MF is
	//never written back.
	AllowVersionUpdate bool
	SkipCheck          bool
	Bump               string
	SetVersion         string
	OutputDir          string
}

//packResult reports what was found in the tenant and what has been packed
type packResult struct {
	ArtifactId string
	//Id the artifact will carry in the target tenant, suffix included
	TargetArtifactId string
	TenantVersion string
	LocalVersion  string
	PackedVersion string
	ZipPath       string
	Bumped        bool
	Zip           []byte
}

func artifactPack(paths []string) {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	//Without --target-env the original environment is both the environment the
	//version is checked against and the one that may be written back to
	targetEnvironment := globalLandscape.OriginalEnvironment
	if *packTargetEnv != "" {
		environment, err := globalLandscape.GetEnvironment(*packTargetEnv)
		if err != nil {
			log.Fatalln(err)
		}
		targetEnvironment = environment
	}

	isOriginal := targetEnvironment.Id == globalLandscape.OriginalEnvironment.Id
	warnIgnoredVersionFlags(isOriginal, targetEnvironment, *packBump, *packSetVersion)

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "#\tArtefactId\tVersion in %s\tLocal Version\tPacked Version\tChanged\tArchive\n", targetEnvironment.Id)

	index := 0

	//Rows are written as soon as an artifact is packed, so that a failure
	//halfway through still reports what has already been produced
	for _, path := range paths {
		index++

		source := filepath.Clean(path)
		artifactId := artifactIdFromPath(source)

		options := packOptions{
			SourceDir:          source,
			Suffix:             targetEnvironment.Suffix,
			AllowVersionUpdate: isOriginal,
			SkipCheck:          *packSkipVersionCheck,
			Bump:               *packBump,
			SetVersion:         *packSetVersion,
			OutputDir:          *packOutputDir,
			TenantArtId:        artifactId + targetEnvironment.Suffix,
			TenantVersion:      versionNotInTenant,
		}

		if !options.SkipCheck {
			packageId, err := resolvePackageId(artifactId, targetEnvironment)
			if err != nil {
				writer.Flush()
				log.Fatalln(err)
			}
			options.CheckEnvironment = targetEnvironment
			options.PackageId = packageId
			options.TenantVersion = readTenantArtifactVersion(targetEnvironment, packageId, options.TenantArtId)
		}

		result, err := packArtifact(options)
		if err != nil {
			writer.Flush()
			log.Fatalln(err)
		}

		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%t\t%s\n",
			index, result.TargetArtifactId, result.TenantVersion, result.LocalVersion,
			result.PackedVersion, result.Bumped, result.ZipPath)
	}

	writer.Flush()
}

//artifactIdFromPath derives the artifact identifier from a folder or archive
//path. The folder name is authoritative, matching how the artifacts are stored
//in the repository.
func artifactIdFromPath(path string) string {
	name := filepath.Base(filepath.Clean(path))
	return strings.TrimSuffix(name, filepath.Ext(name))
}

//resolvePackageId returns the package identifier the artifact belongs to in the
//given environment. The global --pkg flag wins, and it already carries the
//suffix of --env applied in root.go, so it is used as is.
func resolvePackageId(artifactId string, env *landscape.Environment) (string, error) {
	if *pkg != "" {
		return *pkg, nil
	}

	packageId, err := globalLandscape.FindPackageForArtifact(artifactId)
	if err != nil {
		return "", err
	}

	return packageId + env.Suffix, nil
}

//readTenantArtifactVersion returns the version of an artifact in a tenant, or
//versionNotInTenant when the package or the artifact does not exist there.
//The connection is verified first, so that an unreachable tenant fails the run
//instead of silently looking like an empty one.
func readTenantArtifactVersion(env *landscape.Environment, packageId string, artifactId string) string {

	if err := env.System.Client.CheckConnection(); err != nil {
		log.Fatalf("Environment %s is not reachable: %s", env.Id, err)
	}

	artifacts, err := env.System.Client.ReadIntegrationDesigntimeArtifacts(packageId, false)
	if err != nil {
		log.Printf("Package %s is not available in environment %s, the artifact is treated as new", packageId, env.Id)
		return versionNotInTenant
	}

	for _, artifact := range artifacts {
		if artifact.Id == artifactId {
			return artifact.Version
		}
	}

	return versionNotInTenant
}

//packArtifact performs the version check and builds the archive. It is shared
//by "artifact pack" and "artifact upload".
func packArtifact(options packOptions) (*packResult, error) {

	if !iflow.IsArtifactDir(options.SourceDir) {
		return nil, fmt.Errorf("%s is not an integration flow folder, %s is missing", options.SourceDir, iflow.ManifestPath)
	}

	artifactId := artifactIdFromPath(options.SourceDir)

	symbolicName, err := iflow.ReadBundleSymbolicName(options.SourceDir)
	if err == nil && symbolicName != artifactId {
		log.Printf("Folder name %s does not match Bundle-SymbolicName %s, the folder name is used as the artifact id", artifactId, symbolicName)
	}

	localVersionValue, err := iflow.ReadBundleVersion(options.SourceDir)
	if err != nil {
		return nil, err
	}

	localVersion, err := iflow.ParseVersion(localVersionValue)
	if err != nil {
		return nil, err
	}

	result := &packResult{
		ArtifactId:       artifactId,
		TargetArtifactId: artifactId + options.Suffix,
		TenantVersion:    versionNotInTenant,
		LocalVersion:     localVersionValue,
		PackedVersion:    localVersionValue,
	}

	if options.TenantVersion != "" {
		result.TenantVersion = options.TenantVersion
	}

	if !options.SkipCheck && options.CheckEnvironment != nil {
		if options.AllowVersionUpdate {
			//Original environment: the repository owns the version and may be
			//asked to raise it
			packedVersion, bumped, err := resolvePackedVersion(result.TenantVersion, localVersion, options)
			if err != nil {
				return nil, err
			}

			if bumped {
				if err := iflow.SetBundleVersion(options.SourceDir, packedVersion); err != nil {
					return nil, err
				}
				result.PackedVersion = packedVersion
				result.Bumped = true
			}
		} else {
			//Any other environment: the version is taken from the repository as
			//it is and the working copy is never written
			warnOnVersionMisalignment(result.TenantVersion, localVersion, options)
		}
	}

	//The tenant derives the symbolic name of an artifact from its id. An
	//archive uploaded under a suffixed id therefore has to carry the suffixed
	//Bundle-SymbolicName, or the tenant rejects the next update. This applies
	//to the archive only, never to the repository.
	overrides, err := manifestOverrides(options)
	if err != nil {
		return nil, err
	}

	content, err := iflow.ZipDirWithOverrides(options.SourceDir, overrides)
	if err != nil {
		return nil, err
	}
	result.Zip = content

	outputDir := options.OutputDir
	if outputDir == "" {
		outputDir = defaultArtifactOutputDir
	}
	result.ZipPath = filepath.Join(outputDir, result.TargetArtifactId+".zip")

	if err := iflow.WriteFileAtomic(result.ZipPath, content, 0644); err != nil {
		return nil, err
	}

	return result, nil
}

//warnIgnoredVersionFlags reports that --bump and --set-version do not apply
//outside the original environment, where the version always comes from the
//repository. Written to stdout, like the other version warnings.
func warnIgnoredVersionFlags(isOriginal bool, env *landscape.Environment, bump string, setVersion string) {

	if isOriginal {
		return
	}

	for name, value := range map[string]string{"--bump": bump, "--set-version": setVersion} {
		if value != "" {
			fmt.Printf("WARNING %s is ignored for environment %s, the version is taken from %s as it is\n",
				name, env.Id, iflow.ManifestPath)
		}
	}
}

//manifestOverrides builds the archive-only manifest replacement for a suffixed
//environment. Returns nil when no suffix applies, so the archive is then a
//byte for byte copy of the repository.
func manifestOverrides(options packOptions) (map[string][]byte, error) {

	if options.Suffix == "" {
		return nil, nil
	}

	manifest, err := iflow.ReadManifestBytes(options.SourceDir)
	if err != nil {
		return nil, err
	}

	symbolicName, err := iflow.ReadManifestHeader(options.SourceDir, iflow.HeaderBundleSymbolicName)
	if err != nil {
		return nil, err
	}

	updates := map[string]string{
		iflow.HeaderBundleSymbolicName: iflow.ApplySuffixToSymbolicName(symbolicName, options.Suffix),
	}

	//Keep Bundle-Name in step with the name sent as OData metadata
	if bundleName, err := iflow.ReadBundleName(options.SourceDir); err == nil {
		updates[iflow.HeaderBundleName] = strings.TrimSpace(bundleName + " " + options.Suffix)
	}

	rewritten, err := iflow.RewriteManifestHeaders(manifest, updates)
	if err != nil {
		return nil, err
	}

	return map[string][]byte{iflow.ManifestPath: rewritten}, nil
}

//warnOnVersionMisalignment reports, on stdout, that the version in the
//repository is not ahead of the one in the target environment. It is only a
//warning: the version of a non original environment is whatever the repository
//says, and the upload proceeds.
func warnOnVersionMisalignment(tenantVersionValue string, localVersion iflow.Version, options packOptions) {

	if tenantVersionValue == versionNotInTenant || tenantVersionValue == "Active" {
		return
	}

	tenantVersion, err := iflow.ParseVersion(tenantVersionValue)
	if err != nil {
		return
	}

	if iflow.Compare(localVersion, tenantVersion) > 0 {
		return
	}

	fmt.Printf("WARNING version %s of %s is not higher than version %s already present in environment %s, uploading it anyway\n",
		localVersion.String(), options.TenantArtId, tenantVersionValue, options.CheckEnvironment.Id)
}

//resolvePackedVersion decides which version to pack. It returns the version and
//whether the manifest has to be rewritten.
func resolvePackedVersion(tenantVersionValue string, localVersion iflow.Version, options packOptions) (string, bool, error) {

	//Nothing to compare against, the artifact is new in this tenant
	if tenantVersionValue == versionNotInTenant {
		return localVersion.String(), false, nil
	}

	//An artifact saved as a draft in the tenant has no comparable version
	if tenantVersionValue == "Active" {
		log.Printf("Artifact %s is in Draft state in environment %s, its version cannot be compared",
			options.TenantArtId, options.CheckEnvironment.Id)
		return localVersion.String(), false, nil
	}

	tenantVersion, err := iflow.ParseVersion(tenantVersionValue)
	if err != nil {
		return "", false, fmt.Errorf("unable to read the version of %s in environment %s: %w",
			options.TenantArtId, options.CheckEnvironment.Id, err)
	}

	//The local version is already ahead, pack it unchanged
	if iflow.Compare(localVersion, tenantVersion) > 0 {
		return localVersion.String(), false, nil
	}

	//The suggested version is always ahead of the tenant, not of the local copy
	suggested, err := iflow.Bump(tenantVersion, iflow.BumpPatch)
	if err != nil {
		return "", false, err
	}

	if options.SetVersion != "" {
		requested, err := iflow.ParseVersion(options.SetVersion)
		if err != nil {
			return "", false, err
		}
		if iflow.Compare(requested, tenantVersion) <= 0 {
			return "", false, fmt.Errorf("--set-version %s is not higher than version %s of %s in environment %s",
				options.SetVersion, tenantVersionValue, options.TenantArtId, options.CheckEnvironment.Id)
		}
		return requested.String(), true, nil
	}

	if options.Bump != "" {
		bumped, err := iflow.Bump(tenantVersion, options.Bump)
		if err != nil {
			return "", false, err
		}
		return bumped.String(), true, nil
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", false, fmt.Errorf(
			"version %s of %s is not higher than version %s already present in environment %s. "+
				"Pass --bump patch|minor|major, --set-version X.Y.Z or --skip-version-check",
			localVersion.String(), options.TenantArtId, tenantVersionValue, options.CheckEnvironment.Id)
	}

	return promptForVersion(options, tenantVersionValue, localVersion, tenantVersion, suggested)
}

//promptForVersion asks the user for a new version on the terminal
func promptForVersion(options packOptions, tenantVersionValue string, localVersion iflow.Version,
	tenantVersion iflow.Version, suggested iflow.Version) (string, bool, error) {

	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Artifact %s is at version %s in environment %s, the local version is %s.\n",
		options.TenantArtId, tenantVersionValue, options.CheckEnvironment.Id, localVersion.String())

	for {
		fmt.Printf("Please enter a new version [%s]: ", suggested.String())

		input, err := reader.ReadString('\n')
		if err != nil {
			return "", false, err
		}

		input = strings.TrimSpace(input)
		if input == "" {
			return suggested.String(), true, nil
		}

		requested, err := iflow.ParseVersion(input)
		if err != nil {
			fmt.Println(err)
			continue
		}

		if iflow.Compare(requested, tenantVersion) <= 0 {
			fmt.Printf("Version %s is not higher than %s, which is already in the tenant.\n",
				requested.String(), tenantVersionValue)
			continue
		}

		return requested.String(), true, nil
	}
}
