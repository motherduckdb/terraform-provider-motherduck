package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarehouseExampleModels(t *testing.T) {
	for _, example := range []string{"simple", "layered"} {
		t.Run(example, func(t *testing.T) {
			db, err := sql.Open("duckdb", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Errorf("close warehouse fixture: %v", err)
				}
			})
			exec := func(query string) {
				t.Helper()
				if _, err := db.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			model := func(name string) string {
				t.Helper()
				// #nosec G304 -- example and model filenames are selected by this test.
				data, err := os.ReadFile(filepath.Join("../../../examples/warehouses", example, name))
				if err != nil {
					t.Fatal(err)
				}
				return strings.NewReplacer("${database}", "wh", "${raw_database}", "raw_db", "${transform_database}", "transform_db", "${marts_database}", "marts_db").Replace(string(data))
			}
			assertScalar := func(query, want string) {
				t.Helper()
				var got string
				if err := db.QueryRowContext(t.Context(), query).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("%s = %q, want %q", query, got, want)
				}
			}
			if example == "simple" {
				exec("ATTACH ':memory:' AS wh; CREATE SCHEMA wh.raw; CREATE SCHEMA wh.analytics")
				exec("CREATE TABLE wh.raw.orders(order_id VARCHAR, order_date DATE, amount DECIMAL(18,2), status VARCHAR)")
				exec("CREATE VIEW wh.analytics.daily_revenue AS " + model("daily_revenue.sql.tftpl"))
				exec(model("demo.sql.tftpl"))
				assertScalar("SELECT revenue::VARCHAR FROM wh.analytics.daily_revenue", "125.00")
				assertScalar("SELECT order_count::VARCHAR FROM wh.analytics.daily_revenue", "2")
			} else {
				exec("ATTACH ':memory:' AS raw_db; ATTACH ':memory:' AS transform_db; ATTACH ':memory:' AS marts_db")
				exec("CREATE TABLE raw_db.main.orders(order_id VARCHAR, order_date DATE, amount DECIMAL(18,2), status VARCHAR, source_revision BIGINT)")
				exec("CREATE TABLE marts_db.main.daily_revenue(order_date DATE, order_count BIGINT, revenue DECIMAL(18,2))")
				exec("CREATE VIEW transform_db.main.orders_latest AS " + model("orders_latest.sql.tftpl"))
				exec(model("demo.sql.tftpl"))
				for range 2 {
					exec(model("refresh_marts.sql.tftpl"))
					assertScalar("SELECT revenue::VARCHAR FROM marts_db.main.daily_revenue", "145.00")
					assertScalar("SELECT order_count::VARCHAR FROM marts_db.main.daily_revenue", "2")
					assertScalar("SELECT count(*)::VARCHAR FROM marts_db.main.daily_revenue", "1")
				}
				exec("DELETE FROM raw_db.main.orders")
				exec(model("refresh_marts.sql.tftpl"))
				assertScalar("SELECT count(*)::VARCHAR FROM marts_db.main.daily_revenue", "0")
			}
		})
	}
}
