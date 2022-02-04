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
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/joho/godotenv"
	"golang.org/x/term"
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
	Artifacts map[string]*Artifact
}

type Artifact struct {
	Id string
	Configurations map[string]*Configuration
}

type Configuration struct {
	Environment string
	Parameters []*Parameter
}

type Parameter struct {
	Key string
	Value string
	Type string
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
			Artifacts []struct{
				Id string
				Configurations []struct{
					Environment string
					Parameters []struct{
						Key string
						Value string
						Type string
					}
				}
			}
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



func(landscape *Landscape) GetArtifactConfiguration(environment string, pkg string, artifact string) ([]*Parameter, error) {

	defer func() {
        if err := recover(); err != nil {
            log.Println("panic occurred:", err)
        }
    }()


	config := landscape.Packages[pkg].Artifacts[artifact].Configurations[environment].Parameters

	return config, nil
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
	//log.Println(string(config))

	landscape := LandscapeYAML{}
    err = yaml.Unmarshal(config, &landscape)

	if err != nil {
		return nil, err
	}
	//fmt.Println(string(landscape.Landscape.Packages[0].Artifacts[0].Configurations[0].Parameters[0].Key))
	//fmt.Println(string(landscape.Landscape.Packages[0].Artifacts[0].Configurations[0].Parameters[0].Value))

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


		reader := bufio.NewReader(os.Stdin)

		if login == ""{
			fmt.Printf("Please enter login for system %s:\n", system.Name)
			login, _ = reader.ReadString('\n')
		}
		if password == ""{
			fmt.Printf("Please enter password for system %s:\n", system.Name)
			//password, _ = reader.ReadString('\n')
			bytePassword, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				return  nil , err
			}

			password = string(bytePassword)
		}


		system.Client = cpiclient.NewCPIBasicAuthClient(strings.TrimSpace(login), strings.TrimSpace(password), systemYAML.Host, false)
		
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
		artifacts := make(map[string]*Artifact)
		for _, artifactYAML := range packageYAML.Artifacts {
			configurations := make(map[string]*Configuration)
			for _, configurationYAML := range artifactYAML.Configurations {

				parameters := []*Parameter{}
				for _, parameterYAML := range configurationYAML.Parameters {
					paramType := parameterYAML.Type
					if paramType == "" {
						paramType = "xsd:string"
					}
					parameter := &Parameter{
						Key: parameterYAML.Key,
						Value: parameterYAML.Value,
						Type: paramType,
					}
					parameters = append(parameters, parameter)
					
				}

				configuration := &Configuration{
					Environment: configurationYAML.Environment,
					Parameters: parameters,
				}
				configurations[configuration.Environment] = configuration
			}

			artifact := &Artifact{
				Id: artifactYAML.Id,
				Configurations: configurations,
			}

			artifacts[artifactYAML.Id] = artifact

		}




		
		package_ := &Package{
			Id: packageYAML.Id,
			Artifacts: artifacts,
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
		//fmt.Printf("'%s'\n", environment.Id)
		
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
	
	/*
	if value == "" {
		log.Fatalf("Necessary environment variables do not set neither in .env nor in environment")
	}
	*/
	
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


