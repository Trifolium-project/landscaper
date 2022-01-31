# UNDER DEVELOPMENT, cannot be used yet
# Landscaper - CI/CD support for SAP Cloud Integration

## How to have multiple integration landscapes in one CPI tenant 

Standard CPI landscape consists of two systems - dev and prod. Transport system is only capable of transferring integration packages from one tenant to another. Hosting two or more landscapes in one integration tenant is not supported natively by SAP. Therefore this solution helps to overcome this limitation, and have consistent development - test - prod landscape using two(or even one) integration tenant. Actually, multiple variants are supported for those who have heterogenious integration landscape. You can define number of stages and hosting tenants for each integration package. Therefore it is advised to split you developments by integration packages in order to make convenient setup.


Usage

Integration developer created new package and added several integration flows. He finished the developments and saved all packages as version. "Save as version" triggers special action in CI process, which commits changes to git repository, and pushes changes to next stage. If all parameters are found in gitlab parameters, it is being pushed to 

* Prerequisites
  * Roles and authorization setup by tags 
  * No authorization for developers to deploy directly(only via pipeline jobs in CI tool)
  * Setup of CI tool
    * Gitlab CI
    * Jenkins
    * etc

* Actions
  * Commit to git repository
    * Automatic task based on "Save as version" trigger
  * Perform autotesting and security/static checks(check cpi lint)
    * Automatic task, next after commits
  * Maintain parameters in dev(if any)(?)
  * Deploy to dev environment
  * Move to qa environment
  * Maintain parameters in qa(if any)(?)
  * Deploy to qa environment
  * Move to prod environment
  * Maintain parameters in prod(if any)(?)
  * Deploy to prod environment


For base scenario(DEV->QA transport ):
* Packages + 
  * Read packages + 
  * Create package + 
* iFlows
  * Get iFlows by package + 
  * Download iFlow+
  * Upload iFlow+
  * Deploy iFlow+
* Get token  +
* Configuration +
  * Read configuration +
  * Update configuration +

* Objects
  * Package
  * Integration flow
  * Landscape
  * System
  * Environment
  * Client