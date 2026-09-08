package main

import (
	"strings"
	"testing"
)

func TestValidateAllowedPrefix(t *testing.T) {
	tests := map[string]struct {
		execQuery string
		preQuery  string
		prefix    string
		target    string
		wantErr   string
	}{
		"no prefix disables guard":     {execQuery: `DROP DATABASE "prod"`},
		"scalar ignored":               {execQuery: `SELECT count(*) FROM tables`, prefix: "tf_"},
		"non-mutation with prefix":     {preQuery: `INSTALL httpfs; LOAD httpfs`, prefix: "tf_"},
		"valid prefixed drop":          {execQuery: `DROP DATABASE IF EXISTS "tf_run" CASCADE`, prefix: "tf_"},
		"valid prefixed drop share":    {execQuery: `DROP SHARE IF EXISTS "tf_share"`, prefix: "tf_"},
		"valid prefixed drop secret":   {execQuery: `DROP SECRET IF EXISTS "tf_secret" FROM motherduck`, prefix: "tf_"},
		"valid prefixed create":        {execQuery: `CREATE OR REPLACE SHARE "tf_share" FROM "tf_db" (ACCESS RESTRICTED)`, prefix: "tf_"},
		"valid prefixed alter":         {execQuery: `ALTER DATABASE "tf_db" RENAME TO "tf_db2"`, prefix: "tf_"},
		"quoted identifier with space": {execQuery: `DROP DATABASE IF EXISTS "tf provider single 1" CASCADE`, prefix: "tf"},
		"quoted identifier with escaped quote": {
			execQuery: `DROP DATABASE IF EXISTS "tf_db""quoted" CASCADE`, prefix: "tf_",
		},
		"bare identifier folded to lower case": {execQuery: `DROP DATABASE TF_RUN`, prefix: "tf_"},
		"qualified name checks database":       {execQuery: `DROP TABLE "tf_db"."main"."prod_facts"`, prefix: "tf_"},
		"pre mutation checked":                 {preQuery: `CREATE TABLE "prod" (id INTEGER)`, prefix: "tf_", wantErr: `target "prod"`},
		"mutation missing prefix":              {execQuery: `DROP DATABASE "prod"`, prefix: "tf_", wantErr: `target "prod"`},
		"different prefix rejected":            {execQuery: `DROP DATABASE "tfx_prod"`, prefix: "tf_", wantErr: `target "tfx_prod"`},
		"comment-only match rejected": {
			execQuery: `DROP DATABASE "prod" /* tf_ */`, prefix: "tf_", wantErr: `target "prod"`,
		},
		"line comment match rejected": {
			execQuery: "DROP DATABASE \"prod\" -- tf_\n", prefix: "tf_", wantErr: `target "prod"`,
		},
		"literal match rejected": {
			execQuery: `CREATE SECRET "prod" IN MOTHERDUCK (TYPE S3, KEY_ID 'tf_key')`, prefix: "tf_", wantErr: `target "prod"`,
		},
		"prefix inside name rejected":     {execQuery: `DROP DATABASE "my_tf_db"`, prefix: "tf_", wantErr: `target "my_tf_db"`},
		"second statement checked":        {execQuery: `DROP DATABASE "tf_a"; DROP DATABASE "prod"`, prefix: "tf_", wantErr: `target "prod"`},
		"semicolon inside quotes is safe": {execQuery: `DROP DATABASE "tf_a;b"`, prefix: "tf_"},
		"unsupported mutation rejected":   {execQuery: `INSERT INTO "tf_db".main.t VALUES (1)`, prefix: "tf_", wantErr: "unsupported INSERT"},
		"unsupported drop kind rejected":  {execQuery: `DROP FUNCTION tf_fn`, prefix: "tf_", wantErr: "unsupported DROP shape"},
		"missing name rejected":           {execQuery: `DROP DATABASE IF EXISTS`, prefix: "tf_", wantErr: "missing object name"},
		"literal as name rejected":        {execQuery: `DROP DATABASE 'tf_db'`, prefix: "tf_", wantErr: "string literal"},
		"alter snapshot needs target": {
			execQuery: `ALTER SNAPSHOT 'abc' SET snapshot_name = ''`, prefix: "tf_", wantErr: "pass -allow-target",
		},
		"alter snapshot comment does not satisfy": {
			execQuery: `ALTER SNAPSHOT 'abc' SET snapshot_name = '' /* tf_ */`, prefix: "tf_", wantErr: "pass -allow-target",
		},
		"alter snapshot with prefixed target": {
			execQuery: `ALTER SNAPSHOT 'abc' SET snapshot_name = ''`, prefix: "tf_", target: "tf_snapshot",
		},
		"alter snapshot with wrong target": {
			execQuery: `ALTER SNAPSHOT 'abc' SET snapshot_name = ''`, prefix: "tf_", target: "prod_snapshot", wantErr: `allow-target "prod_snapshot"`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateAllowedPrefix(tc.prefix, tc.target, tc.execQuery, tc.preQuery)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), "mutating SQL rejected") || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q should mention rejection and %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSplitStatements(t *testing.T) {
	got := splitStatements("INSTALL httpfs; LOAD httpfs; -- trailing; comment\nSELECT 'a;b' /* c;d */")
	want := []string{"INSTALL httpfs", "LOAD httpfs", "SELECT 'a;b'"}
	if len(got) != len(want) {
		t.Fatalf("got %d statements %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
}
