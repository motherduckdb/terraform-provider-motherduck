package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func main() {
	execQuery := flag.String("sql", "", "SQL statement to execute")
	scalarQuery := flag.String("scalar", "", "SQL scalar query to print")
	database := flag.String("database", "", "MotherDuck database to attach before running SQL")
	preQuery := flag.String("pre", "", "Optional SQL statement to execute before the main statement")
	allowPrefix := flag.String("allow-prefix", "", "When set, mutating SQL must include this object-name prefix")
	flag.Parse()

	if (*execQuery == "" && *scalarQuery == "") || (*execQuery != "" && *scalarQuery != "") {
		fmt.Fprintln(os.Stderr, "exactly one of -sql or -scalar is required")
		os.Exit(2)
	}
	if err := validateAllowedPrefix(*execQuery, *preQuery, *allowPrefix); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := mdsql.New(ctx, mdsql.Config{
		Token:           os.Getenv("MOTHERDUCK_TOKEN"),
		CustomUserAgent: "terraform-provider-motherduck-mdexec",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() {
		if err := client.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	if *database != "" {
		if err := client.AttachDatabase(ctx, *database); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if *preQuery != "" {
		if err := client.Exec(ctx, *preQuery); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if *execQuery != "" {
		if err := client.Exec(ctx, *execQuery); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	value, err := client.ScalarString(ctx, *scalarQuery)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(value)
}

func validateAllowedPrefix(queries ...string) error {
	prefix := ""
	if len(queries) > 0 {
		prefix = queries[len(queries)-1]
		queries = queries[:len(queries)-1]
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	for _, query := range queries {
		normalized := strings.ToUpper(strings.TrimSpace(query))
		if normalized == "" || !isMutation(normalized) {
			continue
		}
		if !strings.Contains(query, prefix) {
			return fmt.Errorf("mutating SQL rejected: query must include allow-prefix %q", prefix)
		}
	}
	return nil
}

func isMutation(normalized string) bool {
	for _, keyword := range []string{"CREATE ", "DROP ", "ALTER ", "INSERT ", "UPDATE ", "DELETE ", "GRANT ", "REVOKE "} {
		if strings.HasPrefix(normalized, keyword) {
			return true
		}
	}
	return false
}
