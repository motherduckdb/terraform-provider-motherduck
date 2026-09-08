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

// scalarQueries collects repeated -scalar flags. Reusing one connection for
// several reads avoids paying the MotherDuck boot sequence, an extension
// install and a duckling start, once per query.
type scalarQueries []string

func (q *scalarQueries) String() string {
	return strings.Join(*q, "; ")
}

func (q *scalarQueries) Set(value string) error {
	*q = append(*q, value)
	return nil
}

func main() {
	execQuery := flag.String("sql", "", "SQL statement to execute")
	var scalarQueryList scalarQueries
	flag.Var(&scalarQueryList, "scalar", "SQL scalar query to print; repeat to run several on one connection")
	database := flag.String("database", "", "MotherDuck database to attach before running SQL")
	preQuery := flag.String("pre", "", "Optional SQL statement to execute before the main statement")
	allowPrefix := flag.String("allow-prefix", "", "When set, mutating SQL must include this object-name prefix")
	flag.Parse()

	if (*execQuery == "" && len(scalarQueryList) == 0) || (*execQuery != "" && len(scalarQueryList) > 0) {
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

	// One line per query, in flag order, so callers can pair results with the
	// queries they asked for. An empty result still prints its line.
	for _, query := range scalarQueryList {
		value, err := client.ScalarString(ctx, query)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(value)
	}
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
