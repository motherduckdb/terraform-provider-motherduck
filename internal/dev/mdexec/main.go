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
	// Repeated -scalar flags share one connection, so several reads pay the
	// MotherDuck boot sequence (extension install, duckling start) once.
	var scalarQueryList []string
	flag.Func("scalar", "SQL scalar query to print. Repeat to run several on one connection", func(value string) error {
		scalarQueryList = append(scalarQueryList, value)
		return nil
	})
	database := flag.String("database", "", "MotherDuck database to attach before running SQL")
	preQuery := flag.String("pre", "", "Optional SQL statement to execute before the main statement")
	allowPrefix := flag.String("allow-prefix", "", "When set, every mutating SQL statement must target an object whose name starts with this prefix")
	allowTarget := flag.String("allow-target", "", "Name of the object a mutation targets when the statement itself does not name it (for example ALTER SNAPSHOT by id). Checked against -allow-prefix")
	timeout := flag.Duration("timeout", 2*time.Minute, "Total timeout for connection setup and statements")
	flag.Parse()

	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "timeout must be greater than zero")
		os.Exit(2)
	}
	if (*execQuery == "" && len(scalarQueryList) == 0) || (*execQuery != "" && len(scalarQueryList) > 0) {
		fmt.Fprintln(os.Stderr, "exactly one of -sql or -scalar is required")
		os.Exit(2)
	}
	if err := validateAllowedPrefix(*allowPrefix, *allowTarget, append([]string{*execQuery, *preQuery}, scalarQueryList...)...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
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
	for index, query := range scalarQueryList {
		value, err := client.ScalarString(ctx, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scalar query %d/%d failed: %v\n", index+1, len(scalarQueryList), err)
			os.Exit(1)
		}
		fmt.Println(value)
	}
}

// validateAllowedPrefix enforces the -allow-prefix guard. When prefix is set,
// every mutating statement in queries must be a recognized DDL shape whose
// target object name starts with prefix. Comments and string literals cannot
// satisfy the check. Statements that do not name their target (ALTER SNAPSHOT
// by id) require -allow-target, which must itself carry the prefix.
func validateAllowedPrefix(prefix, target string, queries ...string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	for _, query := range queries {
		statements, err := splitStatements(query)
		if err != nil {
			return fmt.Errorf("SQL rejected under allow-prefix: %s", err)
		}
		for _, statement := range statements {
			tokens := tokenize(statement)
			if len(tokens) == 0 {
				continue
			}
			if !isMutation(tokens[0]) {
				// Fail closed: only known read-only statement shapes pass
				// without naming a prefixed target, and they must not embed a
				// mutation (for example WITH ... INSERT or a second verb).
				if !isReadOnlyStarter(tokens[0]) {
					return fmt.Errorf("SQL rejected under allow-prefix: unsupported statement %q: %s", tokens[0].text, statement)
				}
				if verb := embeddedMutation(tokens); verb != "" {
					return fmt.Errorf("SQL rejected under allow-prefix: read-only statement embeds %s: %s", verb, statement)
				}
				continue
			}
			name, needsTarget, err := mutationTarget(tokens)
			if err != nil {
				return fmt.Errorf("mutating SQL rejected: %s: %s", err, statement)
			}
			if needsTarget {
				if strings.TrimSpace(target) == "" {
					return fmt.Errorf("mutating SQL rejected: statement does not name its target, pass -allow-target with a name that starts with allow-prefix %q: %s", prefix, statement)
				}
				if !strings.HasPrefix(target, prefix) {
					return fmt.Errorf("mutating SQL rejected: allow-target %q does not start with allow-prefix %q", target, prefix)
				}
				continue
			}
			if !strings.HasPrefix(name, prefix) {
				return fmt.Errorf("mutating SQL rejected: target %q does not start with allow-prefix %q", name, prefix)
			}
		}
	}
	return nil
}

// isReadOnlyStarter reports whether a statement beginning with this bare
// keyword is one the guard lets through without a prefixed target. ATTACH is
// deliberately absent because "ATTACH 'md:name'" creates a MotherDuck
// database. CALL is absent because MotherDuck table functions can mutate.
func isReadOnlyStarter(first token) bool {
	if first.quoted || first.literal {
		return false
	}
	switch strings.ToUpper(first.text) {
	case "SELECT", "FROM", "WITH", "VALUES", "SHOW", "DESCRIBE", "DESC", "SUMMARIZE", "EXPLAIN",
		"USE", "SET", "RESET", "DETACH", "INSTALL", "LOAD", "PRAGMA":
		return true
	}
	return false
}

// embeddedMutation returns the mutation verb found inside an otherwise
// read-only statement, or "" when none is present. It looks for the shapes
// DuckDB accepts after a CTE or in a compound statement: INSERT INTO,
// DELETE FROM, UPDATE <name> SET, and CREATE/DROP/ALTER/TRUNCATE/GRANT/
// REVOKE/COPY followed by a bare word. Quoted identifiers and string
// literals never match, so a column named "update" is not a false positive.
func embeddedMutation(tokens []token) string {
	bare := func(i int) string {
		if i >= len(tokens) || tokens[i].quoted || tokens[i].literal {
			return ""
		}
		return strings.ToUpper(tokens[i].text)
	}
	isName := func(i int) bool {
		return i < len(tokens) && !tokens[i].literal && (tokens[i].quoted || isBareIdentifier(tokens[i].text))
	}
	for i := range tokens {
		switch bare(i) {
		case "INSERT":
			if bare(i+1) == "INTO" {
				return "INSERT"
			}
		case "DELETE":
			if bare(i+1) == "FROM" {
				return "DELETE"
			}
		case "UPDATE":
			if isName(i+1) && bare(i+2) == "SET" {
				return "UPDATE"
			}
		case "CREATE", "DROP", "ALTER", "TRUNCATE", "GRANT", "REVOKE", "COPY":
			if next := bare(i + 1); next != "" && isBareIdentifier(next) {
				return strings.ToUpper(tokens[i].text)
			}
		}
	}
	return ""
}

var (
	mutationVerbs = map[string]bool{"CREATE": true, "DROP": true, "ALTER": true, "INSERT": true, "UPDATE": true, "DELETE": true, "GRANT": true, "REVOKE": true, "TRUNCATE": true}
	objectKinds   = map[string]bool{"DATABASE": true, "SHARE": true, "SECRET": true, "SCHEMA": true, "TABLE": true, "VIEW": true, "ROLE": true}
)

func isMutation(first token) bool {
	return !first.quoted && !first.literal && mutationVerbs[strings.ToUpper(first.text)]
}

// mutationTarget returns the name of the object a DDL statement targets. For
// qualified names the first component is returned, because live test objects
// are scoped by their database name. needsTarget is true for statement shapes
// that identify the object by something other than its name.
func mutationTarget(tokens []token) (name string, needsTarget bool, err error) {
	keyword := func(i int, want string) bool {
		return i < len(tokens) && !tokens[i].quoted && !tokens[i].literal && strings.EqualFold(tokens[i].text, want)
	}
	identifier := func(i int) (string, error) {
		if i >= len(tokens) {
			return "", fmt.Errorf("missing object name")
		}
		tok := tokens[i]
		if tok.literal {
			return "", fmt.Errorf("object name %q is a string literal, not an identifier", tok.text)
		}
		if tok.quoted {
			return tok.text, nil
		}
		if !isBareIdentifier(tok.text) {
			return "", fmt.Errorf("unexpected token %q where an object name was expected", tok.text)
		}
		// DuckDB folds unquoted identifiers to lower case.
		return strings.ToLower(tok.text), nil
	}

	verb := strings.ToUpper(tokens[0].text)
	i := 1
	switch verb {
	case "DROP":
		if i >= len(tokens) || !objectKinds[strings.ToUpper(tokens[i].text)] {
			return "", false, fmt.Errorf("unsupported DROP shape")
		}
		i++
		if keyword(i, "IF") && keyword(i+1, "EXISTS") {
			i += 2
		}
		name, err = identifier(i)
		return name, false, err
	case "CREATE":
		if keyword(i, "OR") && keyword(i+1, "REPLACE") {
			i += 2
		}
		if i >= len(tokens) || !objectKinds[strings.ToUpper(tokens[i].text)] {
			return "", false, fmt.Errorf("unsupported CREATE shape")
		}
		i++
		if keyword(i, "IF") && keyword(i+1, "NOT") && keyword(i+2, "EXISTS") {
			i += 3
		}
		name, err = identifier(i)
		return name, false, err
	case "ALTER":
		if keyword(i, "SNAPSHOT") {
			return "", true, nil
		}
		if i >= len(tokens) || !objectKinds[strings.ToUpper(tokens[i].text)] {
			return "", false, fmt.Errorf("unsupported ALTER shape")
		}
		name, err = identifier(i + 1)
		return name, false, err
	}
	return "", false, fmt.Errorf("unsupported %s statement under allow-prefix", verb)
}

func isBareIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for i, r := range text {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

type token struct {
	text    string
	quoted  bool // "double-quoted" identifier, unescaped
	literal bool // 'single-quoted' string literal, unescaped
}

// splitStatements splits on semicolons outside quotes and comments. Block
// comments that contain another "/*" are rejected rather than parsed: DuckDB
// nests block comments, so a scanner that stopped at the first "*/" would
// expose text DuckDB still treats as a comment, and vice versa. Unterminated
// block comments are rejected for the same reason.
func splitStatements(query string) ([]string, error) {
	var statements []string
	var current strings.Builder
	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\'' || r == '"':
			end := skipQuoted(runes, i, r)
			current.WriteString(string(runes[i:end]))
			i = end - 1
		case r == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			current.WriteRune(' ')
		case r == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			closed := false
			for i+1 < len(runes) {
				if runes[i] == '/' && runes[i+1] == '*' {
					return nil, fmt.Errorf("nested block comment is not supported")
				}
				if runes[i] == '*' && runes[i+1] == '/' {
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i++
			current.WriteRune(' ')
		case r == ';':
			statements = append(statements, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	statements = append(statements, current.String())
	clean := make([]string, 0, len(statements))
	for _, statement := range statements {
		if strings.TrimSpace(statement) != "" {
			clean = append(clean, strings.TrimSpace(statement))
		}
	}
	return clean, nil
}

// skipQuoted returns the index just past the closing quote starting at start,
// honoring doubled quote escapes. An unterminated quote runs to the end.
func skipQuoted(runes []rune, start int, quote rune) int {
	i := start + 1
	for i < len(runes) {
		if runes[i] == quote {
			if i+1 < len(runes) && runes[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(runes)
}

// tokenize splits one comment-free statement into words, quoted identifiers,
// string literals, and single-character punctuation.
func tokenize(statement string) []token {
	var tokens []token
	runes := []rune(statement)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch r {
		case ' ', '\t', '\n', '\r':
			i++
		case '"', '\'':
			end := skipQuoted(runes, i, r)
			var body string
			if end-1 > i && runes[end-1] == r {
				body = string(runes[i+1 : end-1])
			} else {
				// Unterminated quote: take the rest of the statement.
				body = string(runes[i+1:])
			}
			body = strings.ReplaceAll(body, string([]rune{r, r}), string(r))
			tokens = append(tokens, token{text: body, quoted: r == '"', literal: r == '\''})
			i = end
		case '.', '(', ')', ',', '=':
			tokens = append(tokens, token{text: string(r)})
			i++
		default:
			start := i
			for i < len(runes) && !strings.ContainsRune(" \t\n\r\"'.(),=", runes[i]) {
				i++
			}
			tokens = append(tokens, token{text: string(runes[start:i])})
		}
	}
	return tokens
}
