package resources

import (
	"context"
	stdsql "database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// localTableClient runs the table resource against a local DuckDB, whose
// DDL and catalog behave like MotherDuck's for native tables.
type localTableClient struct {
	duckDBCatalogClient
}

func (c *localTableClient) Available() bool { return true }

func (c *localTableClient) AttachDatabase(context.Context, string) error { return nil }

func (c *localTableClient) Exec(ctx context.Context, query string, args ...any) error {
	_, err := c.db.ExecContext(ctx, query, args...)
	return err
}

func (c *localTableClient) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	var value stdsql.NullString
	err := c.db.QueryRowContext(ctx, query, args...).Scan(&value)
	return value.String, err
}

type tableTestCase struct {
	columns     map[string]string
	columnOrder []string // nil leaves it unset
	primaryKey  []string // nil leaves it unset
	notNull     []string // nil leaves it unset
}

func (c tableTestCase) model(unset func() (types.List, types.Set)) tableModel {
	columns := map[string]attr.Value{}
	for name, columnType := range c.columns {
		columns[name] = types.StringValue(columnType)
	}
	unsetList, unsetSet := unset()
	model := tableModel{
		ID: types.StringUnknown(), Database: types.StringValue("memory"), Schema: types.StringValue("main"), Name: types.StringValue("facts"),
		Columns: types.MapValueMust(types.StringType, columns), ColumnOrder: unsetList, PrimaryKey: unsetList, NotNull: unsetSet,
	}
	if c.columnOrder != nil {
		model.ColumnOrder = stringListValue(c.columnOrder)
	}
	if c.primaryKey != nil {
		model.PrimaryKey = stringListValue(c.primaryKey)
	}
	if c.notNull != nil {
		model.NotNull = stringSetValue(c.notNull)
	}
	return model
}

func nullShape() (types.List, types.Set) {
	return types.ListNull(types.StringType), types.SetNull(types.StringType)
}

func unknownShape() (types.List, types.Set) {
	return types.ListUnknown(types.StringType), types.SetUnknown(types.StringType)
}

type tableHarness struct {
	t      *testing.T
	ctx    context.Context
	db     *stdsql.DB
	r      *tableResource
	schema resource.SchemaResponse
	state  tfsdk.State
}

func newTableHarness(t *testing.T) *tableHarness {
	t.Helper()
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	h := &tableHarness{t: t, ctx: t.Context(), db: db}
	h.r = &tableResource{baseResource: baseResource{provider: &providerctx.Context{SQL: &localTableClient{duckDBCatalogClient{db: db}}}}}
	h.r.Schema(h.ctx, resource.SchemaRequest{}, &h.schema)
	return h
}

func (h *tableHarness) create(c tableTestCase) {
	h.t.Helper()
	plan := tfsdk.Plan{Schema: h.schema.Schema}
	if diags := plan.Set(h.ctx, c.model(unknownShape)); diags.HasError() {
		h.t.Fatal(diags)
	}
	resp := resource.CreateResponse{State: emptyResourceState(h.ctx, h.t, h.r)}
	h.r.Create(h.ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatalf("create: %v", resp.Diagnostics)
	}
	h.state = resp.State
	h.refresh()
}

func (h *tableHarness) refresh() tableModel {
	h.t.Helper()
	resp := resource.ReadResponse{State: h.state}
	h.r.Read(h.ctx, resource.ReadRequest{State: h.state}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatalf("read: %v", resp.Diagnostics)
	}
	h.state = resp.State
	return h.model()
}

func (h *tableHarness) model() tableModel {
	h.t.Helper()
	var model tableModel
	if diags := h.state.Get(h.ctx, &model); diags.HasError() {
		h.t.Fatal(diags)
	}
	return model
}

// plan runs ModifyPlan for a configuration and returns the planned model and
// the attributes that require replacement.
func (h *tableHarness) plan(c tableTestCase) (tfsdk.Plan, []string) {
	h.t.Helper()
	config := tfsdk.Config{Schema: h.schema.Schema}
	configState := tfsdk.State{Schema: h.schema.Schema}
	configModel := c.model(nullShape)
	configModel.ID = types.StringNull()
	if diags := configState.Set(h.ctx, configModel); diags.HasError() {
		h.t.Fatal(diags)
	}
	config.Raw = configState.Raw
	plan := tfsdk.Plan{Schema: h.schema.Schema}
	planned := c.model(unknownShape)
	planned.ID = h.model().ID
	if diags := plan.Set(h.ctx, planned); diags.HasError() {
		h.t.Fatal(diags)
	}
	resp := resource.ModifyPlanResponse{Plan: plan}
	h.r.ModifyPlan(h.ctx, resource.ModifyPlanRequest{State: h.state, Plan: plan, Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatalf("plan: %v", resp.Diagnostics)
	}
	replace := make([]string, 0, len(resp.RequiresReplace))
	for _, attribute := range resp.RequiresReplace {
		replace = append(replace, attribute.String())
	}
	return resp.Plan, replace
}

func (h *tableHarness) apply(plan tfsdk.Plan) tableModel {
	h.t.Helper()
	var planned tableModel
	if diags := plan.Get(h.ctx, &planned); diags.HasError() {
		h.t.Fatal(diags)
	}
	resp := resource.UpdateResponse{State: h.state}
	h.r.Update(h.ctx, resource.UpdateRequest{State: h.state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatalf("update: %v", resp.Diagnostics)
	}
	h.state = resp.State
	got := h.model()
	// Terraform rejects an apply whose result differs from a known planned value.
	if !planned.ColumnOrder.IsUnknown() && !planned.ColumnOrder.Equal(got.ColumnOrder) {
		h.t.Fatalf("column_order after apply = %v, planned %v", got.ColumnOrder, planned.ColumnOrder)
	}
	if !planned.PrimaryKey.IsUnknown() && !planned.PrimaryKey.Equal(got.PrimaryKey) {
		h.t.Fatalf("primary_key after apply = %v, planned %v", got.PrimaryKey, planned.PrimaryKey)
	}
	if !planned.NotNull.IsUnknown() && !planned.NotNull.Equal(got.NotNull) {
		h.t.Fatalf("not_null after apply = %v, planned %v", got.NotNull, planned.NotNull)
	}
	if !planned.Columns.Equal(got.Columns) {
		h.t.Fatalf("columns after apply = %v, planned %v", got.Columns, planned.Columns)
	}
	return h.refresh()
}

func (h *tableHarness) scalar(query string) string {
	h.t.Helper()
	var value stdsql.NullString
	if err := h.db.QueryRow(query).Scan(&value); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
	return value.String
}

func tableShapeText(model tableModel) string {
	order, _ := knownStrings(model.ColumnOrder.Elements(), false)
	key, _ := knownStrings(model.PrimaryKey.Elements(), false)
	notNull, _ := knownStrings(model.NotNull.Elements(), false)
	slices.Sort(notNull)
	return "order=" + strings.Join(order, ",") + " pk=" + strings.Join(key, ",") + " not_null=" + strings.Join(notNull, ",")
}

// Adding and dropping columns must keep the table and the rows in its other
// columns. Before this behavior, any column change replaced the table.
func TestTableAddsAndDropsColumnsInPlace(t *testing.T) {
	h := newTableHarness(t)
	base := tableTestCase{columns: map[string]string{"id": "INTEGER", "label": "VARCHAR"}}
	h.create(base)
	if got := tableShapeText(h.model()); got != "order=id,label pk= not_null=" {
		t.Fatalf("created shape = %s", got)
	}
	if _, err := h.db.Exec(`INSERT INTO memory.main.facts VALUES (1, 'kept')`); err != nil {
		t.Fatal(err)
	}

	added := tableTestCase{columns: map[string]string{"id": "INT", "label": "VARCHAR", "amount": "DOUBLE", "active": "BOOLEAN"}}
	plan, replace := h.plan(added)
	if len(replace) != 0 {
		t.Fatalf("adding columns requires replacing %v, want an in-place update", replace)
	}
	if got := tableShapeText(h.apply(plan)); got != "order=id,label,active,amount pk= not_null=" {
		t.Fatalf("shape after adding columns = %s", got)
	}

	dropped := tableTestCase{columns: map[string]string{"id": "INTEGER", "amount": "DOUBLE"}}
	plan, replace = h.plan(dropped)
	if len(replace) != 0 {
		t.Fatalf("dropping columns requires replacing %v, want an in-place update", replace)
	}
	if got := tableShapeText(h.apply(plan)); got != "order=id,amount pk= not_null=" {
		t.Fatalf("shape after dropping columns = %s", got)
	}
	if got := h.scalar(`SELECT id::VARCHAR || '/' || coalesce(amount::VARCHAR, 'null') FROM memory.main.facts`); got != "1/null" {
		t.Fatalf("row after in-place changes = %q, want the original row kept", got)
	}
	if _, replace = h.plan(dropped); len(replace) != 0 {
		t.Fatalf("unchanged configuration requires replacing %v", replace)
	}
}

func TestTableShapeAttributes(t *testing.T) {
	h := newTableHarness(t)
	base := tableTestCase{
		columns:     map[string]string{"id": "INTEGER", "label": "VARCHAR", "region": "VARCHAR"},
		columnOrder: []string{"region", "id", "label"},
		primaryKey:  []string{"id"},
		notNull:     []string{"label"},
	}
	h.create(base)
	if got := tableShapeText(h.model()); got != "order=region,id,label pk=id not_null=label" {
		t.Fatalf("created shape = %s", got)
	}
	if _, err := h.db.Exec(`INSERT INTO memory.main.facts VALUES (NULL, 1, NULL)`); err == nil {
		t.Fatal("a NULL label was accepted, want the NOT NULL constraint")
	}

	// NOT NULL changes and an appended column apply in place.
	changed := base
	changed.columns = map[string]string{"id": "INTEGER", "label": "VARCHAR", "region": "VARCHAR", "amount": "DOUBLE"}
	changed.columnOrder = []string{"region", "id", "label", "amount"}
	changed.notNull = []string{"region"}
	plan, replace := h.plan(changed)
	if len(replace) != 0 {
		t.Fatalf("an appended column and NOT NULL changes require replacing %v", replace)
	}
	if got := tableShapeText(h.apply(plan)); got != "order=region,id,label,amount pk=id not_null=region" {
		t.Fatalf("shape after in-place changes = %s", got)
	}

	for name, tc := range map[string]struct {
		change func(*tableTestCase)
		want   string
	}{
		"column type":         {func(c *tableTestCase) { c.columns["label"] = "INTEGER" }, "columns"},
		"primary key":         {func(c *tableTestCase) { c.primaryKey = []string{"id", "region"} }, "primary_key"},
		"primary key removed": {func(c *tableTestCase) { c.primaryKey = []string{} }, "primary_key"},
		"order of existing":   {func(c *tableTestCase) { c.columnOrder = []string{"id", "region", "label", "amount"} }, "column_order"},
		"column inserted first": {func(c *tableTestCase) {
			c.columns["first"] = "INTEGER"
			c.columnOrder = append([]string{"first"}, c.columnOrder...)
		}, "column_order"},
		"primary key column dropped": {func(c *tableTestCase) {
			delete(c.columns, "id")
			c.columnOrder = []string{"region", "label", "amount"}
			c.primaryKey = nil
		}, "columns"},
		"column renamed by case": {func(c *tableTestCase) {
			delete(c.columns, "label")
			c.columns["LABEL"] = "VARCHAR"
			c.columnOrder = []string{"region", "id", "LABEL", "amount"}
		}, "columns"},
	} {
		c := changed
		c.columns = map[string]string{}
		for column, columnType := range changed.columns {
			c.columns[column] = columnType
		}
		tc.change(&c)
		if _, replace := h.plan(c); !slices.Equal(replace, []string{tc.want}) {
			t.Errorf("%s requires replacing %v, want [%s]", name, replace, tc.want)
		}
	}

	// Omitting the shape attributes keeps what the table has.
	unmanaged := tableTestCase{columns: changed.columns}
	plan, replace = h.plan(unmanaged)
	if len(replace) != 0 {
		t.Fatalf("omitting shape attributes requires replacing %v", replace)
	}
	if got := tableShapeText(h.apply(plan)); got != "order=region,id,label,amount pk=id not_null=region" {
		t.Fatalf("shape after omitting shape attributes = %s", got)
	}
}

func TestTableShapeValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		c    tableTestCase
		want string
	}{
		"order missing a column":    {tableTestCase{columns: map[string]string{"a": "INT", "b": "INT"}, columnOrder: []string{"a"}}, "must list every key"},
		"order with unknown column": {tableTestCase{columns: map[string]string{"a": "INT"}, columnOrder: []string{"a", "z"}}, "not a key of `columns`"},
		"order with a repeat":       {tableTestCase{columns: map[string]string{"a": "INT", "b": "INT"}, columnOrder: []string{"a", "a"}}, "more than once"},
		"key with unknown column":   {tableTestCase{columns: map[string]string{"a": "INT"}, primaryKey: []string{"A"}}, "not a key of `columns`"},
		"not null unknown column":   {tableTestCase{columns: map[string]string{"a": "INT"}, notNull: []string{"z"}}, "not a key of `columns`"},
	} {
		var resp resource.ValidateConfigResponse
		validateTableShape(tc.c.model(nullShape), &resp.Diagnostics)
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), tc.want) {
			t.Errorf("%s diagnostics = %v, want %q", name, resp.Diagnostics, tc.want)
		}
	}
	var resp resource.ValidateConfigResponse
	validateTableShape(tableTestCase{columns: map[string]string{"a": "INT", "b": "INT"}, columnOrder: []string{"b", "a"}, primaryKey: []string{"a"}, notNull: []string{"b"}}.model(nullShape), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		t.Fatalf("valid shape diagnostics = %v", resp.Diagnostics)
	}
}
