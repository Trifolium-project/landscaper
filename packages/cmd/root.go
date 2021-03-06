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

	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string
var landscapeFile *string
var globalLandscape *landscape.Landscape

//Persistent global flag
var (
	environment *string
	pkg         *string
	artifact 	*string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
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
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
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

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {

	_ = godotenv.Load()

	//Set landscape configuration path
	var landscapeFilePath string 
	if *landscapeFile  != "" {
		landscapeFilePath = *landscapeFile 
	} else {
		//Default landscape file location
		landscapeFilePath = "conf/landscape.yaml"
	}

	landscape, err := landscape.NewLandscape(landscapeFilePath)
	if err != nil {
		log.Println(err)	
	} 
	if landscape == nil {
		log.Fatalln("Unable to read landscaper configuration")
	}
	globalLandscape = landscape

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
