---
subcategory: "Unity Catalog"
---
# databricks_views Data Source

-> **Note** If you have a fully automated setup with workspaces created by [databricks_mws_workspaces](../resources/mws_workspaces.md) or [azurerm_databricks_workspace](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/databricks_workspace), please make sure to add [depends_on attribute](../index.md#data-resources-and-authentication-is-not-configured-errors) in order to prevent _authentication is not configured for provider_ errors.

Retrieves a list of view full names in Unity Catalog, that were created by Terraform or manually. Use [databricks_tables](tables.md) for retrieving a list of tables.

## Example Usage

Granting `SELECT` and `MODIFY` to `sensitive` group on all views in a _things_ [databricks_schema](../resources/schema.md) from _sandbox_ [databricks_catalog](../resources/catalog.md).

```hcl
data "databricks_views" "things" {
  catalog_name = "sandbox"
  schema_name  = "things"
}

resource "databricks_grants" "things" {
  for_each = data.databricks_views.things.ids

  view = each.value

  grant {
    principal  = "sensitive"
    privileges = ["SELECT", "MODIFY"]
  }
}
```

## Argument Reference

* `catalog_name` - (Required) Name of [databricks_catalog](../resources/catalog.md)
* `schema_name` - (Required) Name of [databricks_schema](../resources/schema.md)

## Attribute Reference

This data source exports the following attributes:

* `ids` - set of [databricks_table](../resources/table.md) full names: *`catalog`.`schema`.`view`*

## Related Resources

The following resources are used in the same context:

* [databricks_table](../resources/table.md) to manage tables within Unity Catalog.
* [databricks_schema](../resources/schema.md) to manage schemas within Unity Catalog.
* [databricks_catalog](../resources/catalog.md) to manage catalogs within Unity Catalog.
