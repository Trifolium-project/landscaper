# Landscaper - CI/CD support for SAP Cloud Integration

## How to have multiple integration landscapes in one CPI tenant 

Standard CPI landscape consists of two systems - dev and prod. Transport system is only capable of transferring integration packages from one tenant to another. Hosting two or more landscapes in one integration tenant is not supported natively by SAP. Therefore this solution helps to overcome this limitation, and have consistent development - test - prod landscape using two(or even one) integration tenant. Actually, multiple variants are supported for those who have heterogenious integration landscape. You can define number of stages and hosting tenants for each integration package. Therefore it is advised to split you developments by integration packages in order to make convenient setup.


### Usage

 - Create landscape definition, as per [example](./conf/landscape-example.yaml)
 - Start using transport system
```
landscaper package move --target-env=Prod --pkg=SAPHybrisCloudforCustomerIntegrationwithSAPCRM 
```
