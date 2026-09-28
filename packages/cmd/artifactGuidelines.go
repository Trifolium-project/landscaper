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
	"github.com/spf13/cobra"
)

// artifactGuidelinesCmd groups the design guideline commands
var artifactGuidelinesCmd = &cobra.Command{
	Use:   "guidelines",
	Short: "Check integration flows against SAP's design guidelines",
	Long: `Run the design guidelines of SAP Cloud Integration on integration flows, read
the results, and skip rules an artifact does not follow on purpose.

The artifacts are selected with exactly one of:

  --artifacts     artifact ids without environment suffix
  --packages      package ids without environment suffix, read from the tenant
  --all-declared  every artifact of the landscape configuration

The guidelines must be activated for the tenant by an administrator.

Exit codes of run and results:

  0  every artifact is compliant, or nothing reaches --fail-on
  7  a rule at or above --fail-on is not compliant and not skipped
  3  an execution did not finish, or an artifact was never checked
  1  anything else went wrong`,
}

func init() {
	artifactCmd.AddCommand(artifactGuidelinesCmd)
}
