import os

import pulumi
import pulumi_motherduck as motherduck


provider = motherduck.Provider(
    "motherduck",
    token=pulumi.Output.secret(os.environ["MOTHERDUCK_TOKEN"]),
)

database = motherduck.Database(
    "database",
    name="pulumi_example",
    snapshot_retention_days=7,
    opts=pulumi.ResourceOptions(provider=provider),
)

schema = motherduck.Schema(
    "schema",
    database=database.name,
    name="app",
    opts=pulumi.ResourceOptions(provider=provider),
)

table = motherduck.Table(
    "table",
    database=database.name,
    schema=schema.name,
    name="facts",
    columns={"id": "INTEGER", "label": "VARCHAR"},
    opts=pulumi.ResourceOptions(provider=provider),
)

pulumi.export("database_name", database.name)
pulumi.export("table_name", table.name)
