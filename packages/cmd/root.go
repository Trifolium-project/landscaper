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
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Trifolium-project/landscaper/packages/auditlog"
	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

//Default landscape file location
const defaultLandscapeFile = "conf/landscape.yaml"

var cfgFile string
var landscapeFile *string
var globalLandscape *landscape.Landscape

//Default folder for audit logs
const defaultLogDir = "logs"

//Persistent global flag
var (
	environment *string
	pkg         *string
	artifact 	*string
	logEnabled  *bool
	logDir      *string
	logFile     *string
	runID       *string
	progressMode *string
)
var progressDone int
var progressTotal int
var progressCommand string

//Audit log of the run. Nil means logging is off, which is the default, and
//every method of the logger is safe to call on a nil value.
var auditLogger *auditlog.Logger

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		recordRunStart(cmd)
	},
	Use:   "landscaper",
	Short: "SAP CPI Client",
	Long: `Landscaper is an CLI tool for managing SAP Cloud Platform Integration tenants.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {

	//A panic unwinds, unlike the os.Exit of log.Fatalln, so the run can still
	//be closed off before the process dies. getCSRFToken panics whenever the
	//tenant answers a token fetch with anything but 2xx.
	defer func() {
		if recovered := recover(); recovered != nil {
			auditLogger.RunEndWithExit("panic", fmt.Sprint(recovered), 1)
			auditLogger.Close()
			panic(recovered)
		}
	}()

	err := rootCmd.Execute()
	if err != nil {
		auditLogger.RunEndWithExit("failed", err.Error(), 1)
		auditLogger.Close()
		os.Exit(1)
	}

	auditLogger.RunEndWithExit("ok", "", 0)
	auditLogger.Close()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.landscaper.yaml)")
	environment = rootCmd.PersistentFlags().String("env", "", "Environemnt")
	landscapeFile = rootCmd.PersistentFlags().String("landscape-file", "", "Path to landscape configuration file")
	pkg = rootCmd.PersistentFlags().String("pkg", "", "Package")

	artifact = rootCmd.PersistentFlags().String("artifact", "", "Artifact Id")

	logEnabled = rootCmd.PersistentFlags().Bool("log", false, "Write an audit log of the run, including every call to the tenant")
	logDir = rootCmd.PersistentFlags().String("log-dir", defaultLogDir, "Folder for the audit log")
	logFile = rootCmd.PersistentFlags().String("log-file", "", "Write the audit log to this exact file")
	runID = rootCmd.PersistentFlags().String("run-id", "", "Caller run ID (or LANDSCAPER_RUN_ID)")
	progressMode = rootCmd.PersistentFlags().String("progress", "", "Progress stream on stderr: json")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {

	_ = godotenv.Load()

	startAuditLog()

	//Set landscape configuration path
	var landscapeFilePath string 
	if *landscapeFile  != "" {
		landscapeFilePath = *landscapeFile 
	} else {
		landscapeFilePath = defaultLandscapeFile
	}

	landscape, err := landscape.NewLandscape(landscapeFilePath)
	if err != nil {
		log.Println(err)	
	} 
	if landscape == nil {
		log.Fatalln("Unable to read landscaper configuration")
	}
	globalLandscape = landscape
	globalLandscape.SetLogger(auditLogger)

	//Set default environment
	if(*environment == "" ){
		*environment = globalLandscape.OriginalEnvironment.Id
	}

	env, err := landscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)	
	}
	
	//Add environment suffix to package name
	if(*pkg != ""){
		*pkg = *pkg + env.Suffix
	}

	//Add environment suffix to artifact name	
	if(*artifact != ""){
		*artifact = *artifact + env.Suffix
	}
	
	//fmt.Println(globalLandscape)
	//log.Println("Read integration packages")
	//packages, _ := globalLandscape.Systems["dev"].Client.ReadIntegrationPackages()

	//for _, pkg := range  packages {
	//	fmt.Println(pkg.Id)
	//}

	//log.Println(cfgFile)
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".landscaper" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".landscaper")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

//exitWith ends the process with a specific code. The commands that report a
//deployment result need codes beyond 0 and 1, and os.Exit runs no deferred
//function, so the audit log has to be closed off here.
func exitWith(code int) {

	status := "ok"
	if code != 0 {
		status = "failed"
	}
	auditLogger.RunEndWithExit(status, fmt.Sprintf("exit code %d", code), code)
	auditLogger.Close()

	os.Exit(code)
}

//startAuditLog opens the log file when --log was given. Failure is fatal: the
//user asked for a record of what this run did to a tenant, and performing the
//operation without one is worse than not performing it.
func startAuditLog() {

	if (logEnabled == nil || !*logEnabled) && (logFile == nil || *logFile == "") {
		return
	}

	directory := defaultLogDir
	if logDir != nil && *logDir != "" {
		directory = *logDir
	}

	var logger *auditlog.Logger
	var err error
	if logFile != nil && *logFile != "" {
		logger, err = auditlog.NewAt(*logFile)
	} else {
		logger, err = auditlog.New(directory)
	}
	if err != nil {
		log.Fatalln(err)
	}
	auditLogger = logger
	selectedRunID := os.Getenv("LANDSCAPER_RUN_ID")
	if runID != nil && *runID != "" {
		selectedRunID = *runID
	}
	auditLogger.SetContext(selectedRunID, os.Getenv("TRACEPARENT"))

	//Everything the program already writes with the standard logger, including
	//all of the log.Fatalln exits, is recorded without changing those callers
	log.SetOutput(auditLogger.LogWriter(os.Stderr))

	notice := fmt.Sprintf("Writing the audit log to %s\n", auditLogger.Path())
	if jsonOutputRequested() {
		fmt.Fprint(os.Stderr, notice)
	} else {
		fmt.Print(notice)
	}
}

func jsonOutputRequested() bool {
	for index, arg := range os.Args {
		if arg == "--output=json" || (arg == "--output" && index+1 < len(os.Args) && os.Args[index+1] == "json") {
			return true
		}
	}
	return false
}

//recordRunStart writes the command and the parameters it was given. It runs as
//a cobra hook rather than from initConfig, which has no access to the command
//or to the flags that were actually set.
func recordRunStart(cmd *cobra.Command) {

	if cmd == nil {
		return
	}
	progressCommand = strings.TrimPrefix(cmd.CommandPath(), "landscaper ")
	if auditLogger == nil {
		return
	}

	flags := map[string]string{}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		flags[flag.Name] = flag.Value.String()
	})

	selectedEnvironment := ""
	if environment != nil {
		selectedEnvironment = *environment
	}

	auditLogger.RunStart(cmd.CommandPath(), selectedEnvironment, auditlog.RedactFlags(flags))
}

//auditItem records the outcome of one artifact or package. Safe to call when
//logging is off, and safe in tests, where initConfig never runs.
func auditItem(fields map[string]interface{}) {
	auditLogger.Item(fields)
	if progressCommand == "artifact download" {
		return //download emits rows as each artifact finishes
	}
	emitProgress(fields)
}

func emitProgress(fields map[string]interface{}) {
	if progressMode == nil || *progressMode != "json" {
		return
	}
	progressDone++
	total := progressTotal
	if total < progressDone {
		total = progressDone
	}
	status, _ := fields["status"].(string)
	outcome := "ok"
	if failed, _ := fields["failed"].(bool); failed || strings.HasPrefix(status, "failed") {
		outcome = "failed"
	} else if skipped, _ := fields["not_downloadable"].(bool); skipped || strings.HasPrefix(status, "skipped") {
		outcome = "skipped"
	}
	item := fmt.Sprint(fields["artifact"])
	if pkg, ok := fields["package"].(string); ok && pkg != "" {
		item = pkg + "/" + item
	}
	id := os.Getenv("LANDSCAPER_RUN_ID")
	if runID != nil && *runID != "" {
		id = *runID
	}
	line, _ := json.Marshal(map[string]interface{}{
		"type": "progress", "ts": time.Now().UTC().Format(time.RFC3339),
		"run_id": id, "command": progressCommand, "done": progressDone,
		"total": total, "item": item, "status": outcome, "bytes": 0,
	})
	fmt.Fprintln(os.Stderr, string(line))
}
