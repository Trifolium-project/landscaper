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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	guidelinesRulesArtifacts *[]string
	guidelinesRulesVersion   *string
	guidelinesRulesOutput    *string
)

//guidelineRuleEntry is one rule of the catalogue
type guidelineRuleEntry struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Severity string `json:"severity"`
}

// artifactGuidelinesRulesCmd represents the artifact guidelines rules command
var artifactGuidelinesRulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "List the design guideline rules activated for the tenant",
	Long: `List the design guideline rules the tenant checks: id, name, category and
severity.

The API has no catalogue of its own, the DesignGuidelines entity set of the
metadata answers 404. Every execution lists all activated rules however, so the
catalogue is read from the latest execution of the given artifacts. An artifact
that was never checked is checked first.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGuidelinesRules()
	},
}

func init() {
	artifactGuidelinesCmd.AddCommand(artifactGuidelinesRulesCmd)

	guidelinesRulesArtifacts = artifactGuidelinesRulesCmd.Flags().StringSlice("artifacts", []string{}, "Artifact ids without environment suffix, whose executions list the rules")
	guidelinesRulesVersion = artifactGuidelinesRulesCmd.Flags().String("version", guidelineVersionActive, "Artifact version, the tenant only holds the current one")
	guidelinesRulesOutput = artifactGuidelinesRulesCmd.Flags().String("output", outputText, "Output format: text or json")
	artifactGuidelinesRulesCmd.MarkFlagRequired("artifacts")
}

func artifactGuidelinesRules() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	if err := validateGuidelineSelection(guidelineSelection{Artifacts: *guidelinesRulesArtifacts}); err != nil {
		log.Fatalln(err)
	}
	if *guidelinesRulesOutput != outputText && *guidelinesRulesOutput != outputJSON {
		log.Fatalf("Unknown output format %q, use %s or %s", *guidelinesRulesOutput, outputText, outputJSON)
	}

	environment_, err := globalLandscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)
	}
	client := environment_.System.Client

	targets, err := resolveGuidelineTargets(client, environment_, guidelineSelection{Artifacts: *guidelinesRulesArtifacts})
	if err != nil {
		log.Fatalln(err)
	}

	rules, err := collectGuidelineRules(client, targets, *guidelinesRulesVersion)
	if err != nil {
		log.Fatalln(err)
	}

	if *guidelinesRulesOutput == outputJSON {
		encoded, err := json.MarshalIndent(rules, "", "  ")
		if err != nil {
			log.Fatalln(err)
		}
		fmt.Println(string(encoded))
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', 0)
	fmt.Fprintf(writer, "#\tRule\tSeverity\tCategory\tName\n")
	for index, rule := range rules {
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\n", index+1, rule.Id, rule.Severity, rule.Category, rule.Name)
	}
	writer.Flush()
}

//collectGuidelineRules merges the rules of the latest execution of every
//target, sorted by id
func collectGuidelineRules(client guidelineClient, targets []*guidelineTarget, version string) ([]*guidelineRuleEntry, error) {

	byId := map[string]*guidelineRuleEntry{}

	for _, target := range targets {
		execution, err := ensureGuidelineExecution(client, target.ArtifactId, version)
		if err != nil {
			return nil, fmt.Errorf("%s: %s", target.ArtifactId, err)
		}

		_, guidelines, err := client.ReadDesignGuidelineExecutionResult(target.ArtifactId, version, execution.ExecutionId)
		if err != nil {
			return nil, fmt.Errorf("%s: %s", target.ArtifactId, err)
		}

		for _, guideline := range guidelines {
			if byId[guideline.GuidelineId] == nil {
				byId[guideline.GuidelineId] = &guidelineRuleEntry{
					Id:       guideline.GuidelineId,
					Name:     guideline.GuidelineName,
					Category: guideline.Category,
					Severity: guideline.Severity,
				}
			}
		}
	}

	rules := []*guidelineRuleEntry{}
	for _, rule := range byId {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Id < rules[j].Id })

	return rules, nil
}
