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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

//Page size used when reading collections that may be paginated by the tenant
const collectionPageSize = 500

//Safely read a string field from a parsed OData JSON object.
//Tenants return JSON null for empty fields like Description or ShortText,
//so a direct type assertion would panic.
func jsonString(object map[string]interface{}, key string) string {
	value, exists := object[key]
	if !exists || value == nil {
		return ""
	}

	switch typedValue := value.(type) {
	case string:
		return typedValue
	case bool:
		return strconv.FormatBool(typedValue)
	case float64:
		return strconv.FormatFloat(typedValue, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", typedValue)
	}
}

//Safely read a boolean field from a parsed OData JSON object
func jsonBool(object map[string]interface{}, key string) bool {
	value, exists := object[key]
	if !exists || value == nil {
		return false
	}

	switch typedValue := value.(type) {
	case bool:
		return typedValue
	case string:
		parsedValue, err := strconv.ParseBool(typedValue)
		if err != nil {
			return false
		}
		return parsedValue
	default:
		return false
	}
}

//Build an IntegrationPackage out of a single OData JSON entity
func parseIntegrationPackage(packageJson map[string]interface{}) *IntegrationPackage {
	return &IntegrationPackage{
		Id:                jsonString(packageJson, "Id"),
		Name:              jsonString(packageJson, "Name"),
		Description:       jsonString(packageJson, "Description"),
		ShortText:         jsonString(packageJson, "ShortText"),
		Version:           jsonString(packageJson, "Version"),
		Vendor:            jsonString(packageJson, "Vendor"),
		PartnerContent:    jsonBool(packageJson, "PartnerContent"),
		UpdateAvailable:   jsonBool(packageJson, "UpdateAvailable"),
		Mode:              jsonString(packageJson, "Mode"),
		SupportedPlatform: jsonString(packageJson, "SupportedPlatform"),
		ModifiedBy:        jsonString(packageJson, "ModifiedBy"),
		CreationDate:      jsonString(packageJson, "CreationDate"),
		ModifiedDate:      jsonString(packageJson, "ModifiedDate"),
		CreatedBy:         jsonString(packageJson, "CreatedBy"),
		Products:          jsonString(packageJson, "Products"),
		Keywords:          jsonString(packageJson, "Keywords"),
		Countries:         jsonString(packageJson, "Countries"),
		Industries:        jsonString(packageJson, "Industries"),
		LineOfBusiness:    jsonString(packageJson, "LineOfBusiness"),
		PackageContent:    "",
	}
}

//Read all integration packages of the tenant, following pagination.
//ReadIntegrationPackages returns whatever the first page contains, which is
//not enough to take an inventory of a large tenant.
func (s *CPIClient) ReadAllIntegrationPackages() ([]*IntegrationPackage, error) {

	var integrationPackages []*IntegrationPackage

	for skip := 0; ; skip += collectionPageSize {
		url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages" +
			"?$format=json&$top=" + strconv.Itoa(collectionPageSize) + "&$skip=" + strconv.Itoa(skip))

		req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
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

		root, ok := data["d"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Unexpected response while reading integration packages")
		}
		packageRawList, ok := root["results"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("Unexpected response while reading integration packages")
		}

		for _, element := range packageRawList {
			packageJson, ok := element.(map[string]interface{})
			if !ok {
				continue
			}
			integrationPackages = append(integrationPackages, parseIntegrationPackage(packageJson))
		}

		//Last page reached
		if len(packageRawList) < collectionPageSize {
			break
		}
	}

	return integrationPackages, nil
}
