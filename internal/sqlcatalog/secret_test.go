package sqlcatalog

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

// DuckDB lowers only ASCII letters in secret names, so the lookup must find
// the secret that DDL on the configured name would address, even when lower()
// also matches a secret whose name differs in the case of a non-ASCII letter.
func TestSecretByNameResolvesLikeDuckDB(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE SECRET "MySecret" (TYPE s3, KEY_ID 'my')`,
		`CREATE SECRET "äpfel" (TYPE s3, KEY_ID 'lower')`,
		`CREATE SECRET "Äpfel" (TYPE s3, KEY_ID 'upper')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	for configured, want := range map[string]string{
		"MySecret": "mysecret",
		"MYSECRET": "mysecret",
		"Äpfel":    "Äpfel",
		"ÄPFEL":    "Äpfel",
		"äpfel":    "äpfel",
	} {
		if got := FoldSecretName(configured); got != want {
			t.Errorf("FoldSecretName(%q) = %q, want %q", configured, got, want)
		}
		query, args := SecretByName("name", configured)
		// Temporary secrets live in memory storage, not in MotherDuck.
		query = strings.Replace(query, "storage = 'motherduck'", "storage = 'memory'", 1)
		var got string
		if err := db.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("lookup %q: %v", configured, err)
		}
		if got != want {
			t.Errorf("lookup %q found secret %q, want %q", configured, got, want)
		}
	}
}

// The secrets listing filter keeps only the secret a name resolves to. lower()
// alone would also return "äpfel" for "Äpfel", which DuckDB keeps apart. A
// name with no stored spelling still lists the secrets lower() matches, as
// SecretByName finds them.
func TestSecretNameFilterListsOnlyTheResolvedSecret(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE SECRET "MySecret" (TYPE s3, KEY_ID 'my')`,
		`CREATE SECRET "äpfel" (TYPE s3, KEY_ID 'lower')`,
		`CREATE SECRET "Äpfel" (TYPE s3, KEY_ID 'upper')`,
		`CREATE SECRET "öl" (TYPE s3, KEY_ID 'only_lower')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	for configured, want := range map[string]string{
		"MySecret": "mysecret",
		"MYSECRET": "mysecret",
		"Äpfel":    "Äpfel",
		"ÄPFEL":    "Äpfel",
		"äpfel":    "äpfel",
		"ÖL":       "öl",
		"missing":  "",
	} {
		// Temporary secrets live in memory storage, not in MotherDuck.
		filter := strings.ReplaceAll(SecretNameFilter(configured), "storage = 'motherduck'", "storage = 'memory'")
		query := strings.Replace("SELECT name FROM duckdb_secrets() WHERE storage = 'memory' AND <filter> ORDER BY name", "<filter>", filter, 1)
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("list %q: %v", configured, err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		got := strings.Join(names, ",")
		if got != want {
			t.Errorf("list %q returned %q, want %q", configured, got, want)
		}
	}
}
