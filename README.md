# Landscaper - CI/CD support for SAP Cloud Integration

## How to have multiple integration landscapes in one CPI tenant 

Standard CPI landscape consists of two systems - dev and prod. Transport system is only capable of transferring integration packages from one tenant to another. Hosting two or more landscapes in one integration tenant is not supported natively by SAP. Therefore this solution helps to overcome this limitation, and have consistent development - test - prod landscape using two(or even one) integration tenant. Actually, multiple variants are supported for those who have heterogenious integration landscape. You can define number of stages and hosting tenants for each integration package. Therefore it is advised to split you developments by integration packages in order to make convenient setup.

### Really quick start
TODO

### Usage

 - Create landscape definition, as per [example](./conf/landscape-example.yaml)
 - Start using transport system
```
landscaper package move --target-env=Prod --pkg=SAPHybrisCloudforCustomerIntegrationwithSAPCRM 
```


### Landscape definition

Landscape YAML file consists of multiple objects and relationships between them. Prior using landscaper CLI tool, you need to define basic parameters of your integration landscape, such as CPI systems, integration packages and flows, configuration and so on. Very basic example of Landscape definition can be found [here](./conf/landscape-example.yaml). 
This example describes Acme Corporation integration landscape, that consists of two SAP CPI systems(Development and Production tenant). Production tenant hosts only productive integration flows, and development tenant hosts Dev and QA integration flows simultaneously. Changes are transported from original environment Dev to QA, and then to Prod. Each environment has its own configuration values, that are stored in landscape definition. No more manual export\import of packages and iflows, the process can be automated with known CI/CD engines, if you embed landscaper in pipeline.  


#### **Landscape** 

**landscape** is a container for other objects.

**landscape** has next parameters:
   - name - free text
   - originalEnvironment - ID of "development" environment, where changes in integration flows are performed. After commiting changes(save as version), you can transport new version of integration flows to other environments(e.g. test and production).

```yaml
landscape:
  name: Acme Corporation integration landscape
  originalEnvironment: Dev
  #Other objects...
```

Also landscape includes several other objects:
 - Array of **systems**
 - Array of **packages**
 - Array of **environments**


#### **System**

Landscape must contain one or more SAP CPI systems, which you will use to develop and deploy integration flows. Multiple options are possible:
 - One system for all environments
This can be used in small setups, where you have only one SAP CPI tenant. In such scenario you have to consider performance of system, because intence processes in non-prod environments can affect production integration flows. For example, dev iflow can hang up the whole tenant, which will have negative consequences for production data exchange. Therefore this setup is not recommended for landscapes with large amount of data exchange.

 - One system for each environment
The most expensive and reliable way, where you separate each landscape physically. There is no influence of environments between each other, so that if process in dev system hang whole tenant, you still have working artifacts in Production

 - Mixed scenario
Often times SAP provides two tenants of Cloud Platform Integration: Development and Production. But many enterprise information systems have 3-tier of even 4-tier landscape. In such a case, it seems natural to have all non-prod artifacts in SAP CPI development tenant, and all production artifacts in SAP CPI production tenant. It can be done by hand with export\import packages and artifacts, add some prefixes and deploy new artifacts as separate landscape. In practise, you often need to make an adjustements in artifacts, and each time you want to move changes from Dev to Test, this leads to monotonous manual process of export\import and reconfiguration. In the end of the day, in the sake of speed you make change directly in Test environment, and forget to move change back to Dev. That is how all the mess starts. Landscaper can help you to avoid this, and have separate environments with automatic transport and configuration process. 

In the provided example, this last mixed scenario is described.

Example definition of two systems is provided below:

```yaml
  systems:
    - id: dev
      name: Development Tenant lxxxxxx
      host: exxxxxx-tmn.hci.xxx.hana.ondemand.com
      login: DEV_LOGIN_ENV_VAR
      password: DEV_PASSWORD_ENV_VAR
    - id: prod
      name: Production Tenant Trial
      host: lxxxxxx-tmn.hci.xxx.hana.ondemand.com
      login: PROD_LOGIN_ENV_VAR
      password: PROD_PASSWORD_ENV_VAR
```

Each system have next parameters:
 - id - unique identificator of the system
 - name - free text
 - host - hostname of the SAP CPI tenant
 - login - environment variable, which contains username(S-user). This user should have an access to Cloud Platform Integration API.
 - password - environment variable, which contains password for provided usernamу

Credentials cannot be set directly in landscape file due to security reasons. Please use environment variables, or [.env](./example.env) file. If credentials are not provided, landscaper will ask username and password for each system in interactive way.

#### **Environment**

Environment is an abstract concept, which represents set of packages, related to a specific system.
Relationship between a system and environments is 1:n, so that system can host many environments, but an environment cannot be spread amongst many systems.

Example definition of three environments is provided below:

```yaml
  environments:
    - id: Dev
      name: Development Environment
      suffix: null
      system: dev
    - id: QA
      name: QA Environment
      suffix: QA
      system: dev
    - id: Prod
      name: Prod Environment
      suffix: null
      system: prod
```

Each system have next parameters:

 - id - unique identificator of the environment
 - name - free text
 - suffix - short set of letters, which is used to separate packages and artifacts from different environments, in case they are hosted in one system. This is only useful, if one system hosts more than one environment.
 - system - id of the system, to which this environemnt is assigned.

If you need to add new environment to your landscape, minimum option is to add new entry in this array. For automatic configuration of iflows you should also consider setting up packages and artifacts in landscape definition.

#### **Package**

Similar to SAP CPI, in landscape definition package is only a container for related integration flows and other artifacts. in fact, there is no need to add package to definition, if you will not add iflows' configurations. In this case, all operations with package transport and artifact list\deploy\undeploy can still be performed. 

Each package have next parameters:

 - id - unique identificator of the package. It should be exactly the same, as you see it in CPI. Make sure, that you entered here ID of the package, and not the description.
 - Array of artifacts 

#### **Artifact**

Now, only integration flows are supported, but it is also planned to add other objects. 