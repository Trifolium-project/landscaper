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
package cpiclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
)

const (
	apiVersion = "v1"
)




type CPIClient struct {
	Username    string
	Password    string
	URL         string
	Client      *http.Client
	clientTrace *httptrace.ClientTrace
	traceCtx    context.Context
	VerboseLog	bool
}

type IntegrationPackage struct {
	Id                string
	Name              string
	Description       string
	ShortText         string
	Version           string
	Vendor            string
	PartnerContent    bool
	UpdateAvailable   bool
	Mode              string `json:"-"`
	SupportedPlatform string
	ModifiedBy        string `json:"-"`
	CreationDate      string `json:"-"`
	ModifiedDate      string `json:"-"`
	CreatedBy         string `json:"-"`
	Products          string
	Keywords          string
	Countries         string
	Industries        string
	LineOfBusiness    string
	PackageContent    string
}

type IntegrationDesigntimeArtifact struct {
	Id              string
	Version         string `json:"-"`
	PackageId       string
	Name            string
	Description     string
	Sender          string `json:"-"`
	Receiver        string `json:"-"`
	ArtifactContent string
	Configurations  []*Configuration `json:"-"`
}

type IntegrationRuntimeArtifact struct {
	Id              string
	Version         string
	Name            string
	Type 			string
	DeployedBy		string
	DeployedOn		string
	Status			string
}
/*

{
	"d": {
	  "Id": "IntegrationFlow_MessageStore_COMPLETED_PROCESSING",
	  "Version": "1.0.0",
	  "Name": "Integration Flow with MessageStore - COMPLETED PROCESSING",
	  "Type": "INTEGRATION_FLOW",
	  "DeployedBy": "Tester",
	  "DeployedOn": "/Date(1521463557739)/",
	  "Status": "STARTED",
	  "ErrorInformation": {
		"__deferred": {
		  "uri": "https://sandbox.api.sap.com/cpi/api/v1/IntegrationRuntimeArtifacts('IntegrationFlow_MessageStore_COMPLETED_PROCESSING')/ErrorInformation"
		}
	  }
	}
  }
  */



//Workaround, while JSON response for certain requests is not supported
type IntegrationDesigntimeArtifactXMLEntry struct {
	XMLName    xml.Name                                   `xml:"entry"`
	Properties IntegrationDesigntimeArtifactXMLProperties `xml:"properties"`
}

type IntegrationDesigntimeArtifactXMLProperties struct {
	XMLName     xml.Name `xml:"properties"`
	Id          string   `xml:"Id"`
	Version     string   `xml:"Version"`
	PackageId   string   `xml:"PackageId"`
	Name        string   `xml:"Name"`
	Description string   `xml:"Description"`
	Sender      string   `xml:"Sender"`
	Receiver    string   `xml:"Receiver"`
}

type Configuration struct {
	ParameterKey   string
	ParameterValue string
	DataType       string
}

func NewCPIBasicAuthClient(username, password, url string, verbose bool) *CPIClient {
	clientTrace := &httptrace.ClientTrace{
		//GotConn: func(info httptrace.GotConnInfo) { log.Printf("Connection was reused: %t", info.Reused) },
		//ConnectStart: func(network, addr string) { log.Printf("Connection was started: %s, %s", network, addr) },
		//WroteHeaderField: func(key string, value []string) {log.Printf("Header written : %s, %s", key, value)},
	}
	traceCtx := httptrace.WithClientTrace(context.Background(), clientTrace)

	jar, err := cookiejar.New(nil)
	if err != nil {
		// error handling
	}

	return &CPIClient{
		Username: username,
		Password: password,
		URL:      url,
		Client: &http.Client{
			Jar: jar,
		},
		clientTrace: clientTrace,
		traceCtx:    traceCtx,
		VerboseLog: verbose,
	}
}

func (s *CPIClient) doRequest(req *http.Request) ([]byte, http.Header, error) {
	req.SetBasicAuth(s.Username, s.Password)
	if s.VerboseLog {
		log.Println(req)
		log.Printf("\n\n")
	}
	resp, err := s.Client.Do(req)

	if err != nil {
		log.Printf("HTTP request error: %s", err)
		return nil, nil, err
	}
	//log.Printf("Status code of HTTP request: %d", resp.StatusCode)

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		return nil, nil, err
	}

	if _, err := io.Copy(ioutil.Discard, resp.Body); err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()
	if s.VerboseLog {
		log.Printf("Response: %s", resp)
		log.Printf("\n\n")
	}

	var httpCodeGroup int
	for httpCodeGroup = resp.StatusCode; httpCodeGroup >= 10; httpCodeGroup = httpCodeGroup / 10 {
	}

	if httpCodeGroup != 2 {

		return nil, nil, fmt.Errorf("%s", body)
	}
	return body, resp.Header, nil
}

func (s *CPIClient) getCSRFToken() (string, error) {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "?$format=json")
	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Add("X-CSRF-Token", "Fetch")
	_, headers, err := s.doRequest(req)
	if err != nil {
		return "", err
	}
	//log.Println(headers)
	//log.Println(req.Cookie("__Host-csrf-client-id"))

	return headers[http.CanonicalHeaderKey("X-CSRF-Token")][0], nil

}

//Configuration
func (s *CPIClient) UpdateIntegrationDesigntimeArtifactConfiguration(ArtifactId string, ArtifactVersion string, configuration *Configuration) error {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')/$links/Configurations('" + configuration.ParameterKey + "')")

	body, err := json.Marshal(configuration)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPut, url, bytes.NewBuffer(body))
	//req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}
	req.Header.Add("X-CSRF-Token", token)
	req.Header.Add("Content-Type", "application/json")

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil
}

func (s *CPIClient) ReadIntegrationDesigntimeArtifactConfigurations(ArtifactId string, ArtifactVersion string) ([]*Configuration, error) {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')/Configurations" + "?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}

	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	root := data["d"].((map[string]interface{}))
	configurationsRawList := root["results"].([]interface{})

	var configurations []*Configuration
	var configuration *Configuration

	for _, element := range configurationsRawList {
		artifactJson := element.(map[string]interface{})
		configuration = &Configuration{
			ParameterKey:   artifactJson["ParameterKey"].(string),
			ParameterValue: artifactJson["ParameterValue"].(string),
			DataType:       artifactJson["DataType"].(string),
		}
		configurations = append(configurations, configuration)
	}
	return configurations, nil

}


//IntegrationRuntimeArtifacts
func (s *CPIClient) ReadIntegrationRuntimeArtifact(ArtifactId string) (*IntegrationRuntimeArtifact, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationRuntimeArtifacts('" +
		ArtifactId + "')" + "?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}

	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	root := data["d"].((map[string]interface{}))

	integrationArtifact := &IntegrationRuntimeArtifact{
		Id:                root["Id"].(string),
		Version:		   root["Version"].(string),
		Name:              root["Name"].(string),
		Type:			   root["Type"].(string),
		DeployedBy:		   root["DeployedBy"].(string),
		DeployedOn:		   root["DeployedOn"].(string),
		Status:		       root["Status"].(string),

	}
	
	return integrationArtifact, nil

}

/*
type IntegrationRuntimeArtifact struct {
	Id              string
	Version         string
	Name            string
	Type 			string
	DeployedBy		string
	DeployedOn		string
	Status			string
}
	


{
	"d": {
	  "Id": "IntegrationFlow_MessageStore_COMPLETED_PROCESSING",
	  "Version": "1.0.0",
	  "Name": "Integration Flow with MessageStore - COMPLETED PROCESSING",
	  "Type": "INTEGRATION_FLOW",
	  "DeployedBy": "Tester",
	  "DeployedOn": "/Date(1521463557739)/",
	  "Status": "STARTED",
	  "ErrorInformation": {
		"__deferred": {
		  "uri": "https://sandbox.api.sap.com/cpi/api/v1/IntegrationRuntimeArtifacts('IntegrationFlow_MessageStore_COMPLETED_PROCESSING')/ErrorInformation"
		}
	  }
	}
  }
  */


//IntegrationDesigntimeArtifacts
func (s *CPIClient) ReadIntegrationDesigntimeArtifacts(PackageId string, fetchConfig bool) ([]*IntegrationDesigntimeArtifact, error) {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages('" + PackageId +
		"')/IntegrationDesigntimeArtifacts" + "?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}

	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	root := data["d"].((map[string]interface{}))
	artifactsRawList := root["results"].([]interface{})

	var integrationArtifacts []*IntegrationDesigntimeArtifact
	var integrationArtifact *IntegrationDesigntimeArtifact

	for _, element := range artifactsRawList {
		artifactJson := element.(map[string]interface{})
		integrationArtifact = &IntegrationDesigntimeArtifact{
			Id:              artifactJson["Id"].(string),
			Version:         artifactJson["Version"].(string),
			PackageId:       artifactJson["PackageId"].(string),
			Name:            artifactJson["Name"].(string),
			Description:     artifactJson["Description"].(string),
			Sender:          artifactJson["Sender"].(string),
			Receiver:        artifactJson["Receiver"].(string),
			ArtifactContent: "",
		}
		if fetchConfig {
			integrationArtifact.Configurations, _ = s.ReadIntegrationDesigntimeArtifactConfigurations(
				integrationArtifact.Id, integrationArtifact.Version,
			)
		}

		integrationArtifacts = append(integrationArtifacts, integrationArtifact)
	}
	return integrationArtifacts, nil

}

func (s *CPIClient) DownloadIntegrationDesigntimeArtifact(ArtifactId string, ArtifactVersion string) (*IntegrationDesigntimeArtifact, error) {

	integrationArtifact, err := s.ReadIntegrationDesigntimeArtifact(ArtifactId, ArtifactVersion)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')/$value")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}
	//TODO: Perform check for unsuccessful download, and return error

	integrationArtifact.ArtifactContent = base64.StdEncoding.EncodeToString(bytes)

	return integrationArtifact, nil

}

func (s *CPIClient) ReadIntegrationDesigntimeArtifact(ArtifactId string, ArtifactVersion string) (*IntegrationDesigntimeArtifact, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	//var data map[string]interface{}
	var dataXML IntegrationDesigntimeArtifactXMLEntry

	err = xml.Unmarshal(bytes, &dataXML)
	if err != nil {
		return nil, err
	}

	//var integrationArtifact *IntegrationDesigntimeArtifact

	integrationArtifact := &IntegrationDesigntimeArtifact{
		Id:              dataXML.Properties.Id,
		Version:         dataXML.Properties.Version,
		PackageId:       dataXML.Properties.PackageId,
		Name:            dataXML.Properties.Name,
		Description:     dataXML.Properties.Description,
		Sender:          dataXML.Properties.Sender,
		Receiver:        dataXML.Properties.Receiver,
		ArtifactContent: "",
	}
	integrationArtifact.Configurations, _ = s.ReadIntegrationDesigntimeArtifactConfigurations(
		integrationArtifact.Id, integrationArtifact.Version,
	)

	return integrationArtifact, nil
	//return integrationArtifact, nil

}

func (s *CPIClient) UploadIntegrationDesigntimeArtifact(integrationArtifact *IntegrationDesigntimeArtifact) error {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts")

	body, err := json.Marshal(integrationArtifact)

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPost, url, bytes.NewBuffer(body))
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", "application/json")

	_, _, err = s.doRequest(req)

	if err != nil {
		return err
	}

	return nil
}

//IntegrationDesigntimeArtifact

func (s *CPIClient) DeployIntegrationDesigntimeArtifact(ArtifactId string, ArtifactVersion string) error {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "DeployIntegrationDesigntimeArtifact?Id='" +
		ArtifactId + "'&Version='" + ArtifactVersion + "'")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPost, url, nil)

	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	req.Header.Set("X-CSRF-Token", token)

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil

}

//Delete artifact
func (s *CPIClient) DeleteIntegrationDesigntimeArtifact(ArtifactId string, ArtifactVersion string) error {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodDelete, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	req.Header.Set("X-CSRF-Token", token)

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil

}


func (s *CPIClient) UndeployIntegrationRuntimeArtifact(ArtifactId string) (error) {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationRuntimeArtifacts(Id='" +
		ArtifactId + "')")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodDelete, url, nil)

	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	req.Header.Set("X-CSRF-Token", token)

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil

}


/*
func (s *CPIClient) undeployIntegrationDesigntimeArtifact(ArtifactId string, ArtifactVersion string ) (error) {

	url := fmt.Sprintf(s.URL + "/DeployIntegrationDesigntimeArtifact?Id='" +
		ArtifactId + "'&Version='" + ArtifactVersion + "'")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodDelete, url, nil)

	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	req.Header.Set("X-CSRF-Token", token)

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil

}
*/

//IntegrationPackages
func (s *CPIClient) ReadIntegrationPackages() ([]*IntegrationPackage, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages" + "?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}

	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	root := data["d"].((map[string]interface{}))
	packageRawList := root["results"].([]interface{})

	var integrationPackages []*IntegrationPackage
	var integrationPackage *IntegrationPackage

	for _, element := range packageRawList {
		packageJson := element.(map[string]interface{})
		integrationPackage = &IntegrationPackage{
			Id:                packageJson["Id"].(string),
			Name:              packageJson["Name"].(string),
			Description:       packageJson["Description"].(string),
			ShortText:         packageJson["ShortText"].(string),
			Version:           packageJson["Version"].(string),
			Vendor:            packageJson["Vendor"].(string),
			PartnerContent:    packageJson["PartnerContent"].(bool),
			UpdateAvailable:   packageJson["UpdateAvailable"].(bool),
			Mode:              packageJson["Mode"].(string),
			SupportedPlatform: packageJson["SupportedPlatform"].(string),
			ModifiedBy:        packageJson["ModifiedBy"].(string),
			CreationDate:      packageJson["CreationDate"].(string),
			ModifiedDate:      packageJson["ModifiedDate"].(string),
			CreatedBy:         packageJson["CreatedBy"].(string),
			Products:          packageJson["Products"].(string),
			Keywords:          packageJson["Keywords"].(string),
			Countries:         packageJson["Countries"].(string),
			Industries:        packageJson["Industries"].(string),
			LineOfBusiness:    packageJson["LineOfBusiness"].(string),
			PackageContent:    "",
		}
		integrationPackages = append(integrationPackages, integrationPackage)
	}
	return integrationPackages, nil
}

//IntegrationPackage by ID
func (s *CPIClient) ReadIntegrationPackage(PackageId string) (*IntegrationPackage, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages('" + PackageId + "')?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	//req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}

	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	root := data["d"].((map[string]interface{}))

	integrationPackage := &IntegrationPackage{
		Id:                root["Id"].(string),
		Name:              root["Name"].(string),
		Description:       root["Description"].(string),
		ShortText:         root["ShortText"].(string),
		Version:           root["Version"].(string),
		Vendor:            root["Vendor"].(string),
		PartnerContent:    root["PartnerContent"].(bool),
		UpdateAvailable:   root["UpdateAvailable"].(bool),
		Mode:              root["Mode"].(string),
		SupportedPlatform: root["SupportedPlatform"].(string),
		ModifiedBy:        root["ModifiedBy"].(string),
		CreationDate:      root["CreationDate"].(string),
		ModifiedDate:      root["ModifiedDate"].(string),
		CreatedBy:         root["CreatedBy"].(string),
		Products:          root["Products"].(string),
		Keywords:          root["Keywords"].(string),
		Countries:         root["Countries"].(string),
		Industries:        root["Industries"].(string),
		LineOfBusiness:    root["LineOfBusiness"].(string),
		PackageContent:    "",
	}

	return integrationPackage, nil
}

/*
func (s *CPIClient) createIntegrationPackage2(integrationPackage *IntegrationPackage2) (error) {
	url := fmt.Sprintf(s.URL + "/IntegrationPackages")

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	http.Post(url,; )
}
*/

func (s *CPIClient) CreateIntegrationPackage(integrationPackage *IntegrationPackage) error {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages")

	body, err := json.Marshal(integrationPackage)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPost, url, bytes.NewBuffer(body))
	//req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}

	//req.Header["x-csrf-token"] = []string{token}
	//req.Header.Del("Accept-Encoding")

	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", "application/json")

	_, _, err = s.doRequest(req)
	if err != nil {
		return err
	}

	return nil
}


func (s *CPIClient) CopyIntegrationPackageFromDiscover(DiscoverPackageId string) (*IntegrationPackage, error) {
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "CopyIntegrationPackage?" + "$format=json" + "&Id='" +  DiscoverPackageId + "'")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPost, url, nil)
	//req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return nil, err
	}

	//req.Header["x-csrf-token"] = []string{token}
	//req.Header.Del("Accept-Encoding")

	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", "application/json")

	bytes, _, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}

	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	root := data["d"].((map[string]interface{}))

	integrationPackage := &IntegrationPackage{
		Id:                root["Id"].(string),
		Name:              root["Name"].(string),
		Description:       root["Description"].(string),
		ShortText:         root["ShortText"].(string),
		Version:           root["Version"].(string),
		/*
		Vendor:            root["Vendor"].(string),
		Mode:              root["Mode"].(string),
		SupportedPlatform: root["SupportedPlatform"].(string),
		ModifiedBy:        root["ModifiedBy"].(string),
		CreationDate:      root["CreationDate"].(string),
		ModifiedDate:      root["ModifiedDate"].(string),
		CreatedBy:         root["CreatedBy"].(string),
		Products:          root["Products"].(string),
		Keywords:          root["Keywords"].(string),
		Countries:         root["Countries"].(string),
		Industries:        root["Industries"].(string),
		LineOfBusiness:    root["LineOfBusiness"].(string),
		PackageContent:    "", 
		*/
	}

	return integrationPackage, nil
}

func (s *CPIClient) CheckConnection() error {

	token, err := s.getCSRFToken()
	
	if err != nil || token == ""{
		log.Printf("System %s check unsuccessful: %s", s.URL, err)

		return err
	}
	//log.Printf("System %s check successful", s.URL)

	return nil
}




func (artifact *IntegrationDesigntimeArtifact) GetConfiguration(parameter string) (*Configuration, error) {

	for _, conf := range artifact.Configurations {
		if conf.ParameterKey == parameter {
			return conf, nil
		}
	}

	return nil, fmt.Errorf("parameter %s is not found in artifact %s", parameter, artifact.Id)

}