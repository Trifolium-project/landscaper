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
package landscape

import (
	"fmt"
	"log"
	"os"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

//import cpiclient
type Landscape struct {
	Name                string
	Systems             map[string]*System
	Packages            map[string]*Package
	Environments        map[string]*Environment
	OriginalEnvironment *Environment
}

type System struct {
	Id     string
	Name   string
	Client *cpiclient.CPIClient
}

type Environment struct {
	Id     string
	Name   string
	Suffix string
	System *System
}

type Package struct {
	Id string
}


type LandscapeYAML struct {
	Landscape struct { 
		Name string
		Systems []struct {
			Id string 
			Name string
			Host string
			Login string
			Password string
		}
		Packages []struct{
			Id string
		}
		Environments []struct{
			Id string
			Name string
			Suffix string
			System string
		}
		OriginalEnvironment string `yaml:"originalEnvironment"`
	}
}

func(landscape *Landscape) GetSystem4Environment(environment *string) (*System, error) {
	
	env := landscape.Environments[*environment]
	if env == nil {
		return nil, fmt.Errorf("Environment %s is not found", *environment)
	}



	return env.System, nil
}

//Get environment by ID
func(landscape *Landscape) GetEnvironment(environment string) (*Environment, error) {
	
	env := landscape.Environments[environment]
	if env == nil {
		return nil, fmt.Errorf("Environment %s is not found", environment)
	}


	return env, nil
}

func NewLandscape(configFile string) (*Landscape, error) {
	_ = godotenv.Load()
	//cmd.Execute()
	if configFile == "" {
		configFile = "conf/landscape-prod.yaml"
	}

	
    
	config, err := os.ReadFile(configFile)
	
	if err != nil {
		return nil, err
    }
	log.Println(string(config))

	landscape := LandscapeYAML{}
    err = yaml.Unmarshal(config, &landscape)

	if err != nil {
		return nil, err
	}
	fmt.Println(string(landscape.Landscape.OriginalEnvironment))

	return buildLandscapeFromManifest(&landscape)
}

func buildLandscapeFromManifest(landscapeYaml *LandscapeYAML) (*Landscape, error) {


	systems :=  map[string]*System{}
	packages :=  map[string]*Package{} 
	environments := map[string]*Environment{} 
	

	//Create systems
	for _, systemYAML := range landscapeYaml.Landscape.Systems {
		system := &System{}
		system.Id = systemYAML.Id
		system.Name = systemYAML.Name
		login, err := getEnvVariableValue(systemYAML.Login)
		if err != nil {
			return nil, err
		}
		password, err := getEnvVariableValue(systemYAML.Password)
		if err != nil {
			return nil, err
		}

		system.Client = cpiclient.NewCPIBasicAuthClient(login, password, systemYAML.Host)
		
		systems[system.Id] = system
		
		
		//TODO: Implement check connection
		/*
		log.Println("Checking connection...")
		
		err = system.Client.CheckConnection()
		if err != nil {
			log.Fatalln(err)
			//return nil, err
		}
		*/
		
	}

	//Create packages
	for _, packageYAML := range landscapeYaml.Landscape.Packages {
		
		package_ := &Package{
			Id: packageYAML.Id,
		}

		packages[package_.Id] = package_
	}

	//Create environments
	for _, environmentYAML := range landscapeYaml.Landscape.Environments {
		
		environment := &Environment{
			Id: environmentYAML.Id,
			Name: environmentYAML.Name,
			Suffix: environmentYAML.Suffix,
			System: systems[environmentYAML.System],
		}
		fmt.Printf("'%s'\n", environment.Id)
		
		environments[environment.Id] = environment
	}



	landscape := &Landscape{
		Name: landscapeYaml.Landscape.Name,
		Systems: systems,
		Packages: packages,
		Environments: environments,
		OriginalEnvironment: environments[landscapeYaml.Landscape.OriginalEnvironment],
	}
	
	return landscape, nil
}

//TODO: Change to viper
func getEnvVariableValue(variableName string) (string, error) {
	value := os.Getenv(variableName)
	log.Println(variableName)

	if value == "" {
		log.Fatalf("Necessary environment variables do not set neither in .env nor in environment")
	}
	
	return value, nil
}

//func getOriginalSystem
//func getSystem

/*
func transportChangedArtifacts(package string) (error) {

	return nil
}

func deployChangedArtifacts(package string) 
*/


