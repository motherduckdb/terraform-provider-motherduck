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
		wantErr   bool
	}{
		"no prefix disabled":       {execQuery: `DROP DATABASE "prod"`, wantErr: false},
		"scalar ignored":           {execQuery: `SELECT count(*) FROM tables`, prefix: "tf_", wantErr: false},
		"mutation includes prefix": {execQuery: `DROP DATABASE "tf_run"`, prefix: "tf_", wantErr: false},
		"pre mutation checked":     {preQuery: `CREATE TABLE "prod" (id INTEGER)`, prefix: "tf_", wantErr: true},
		"mutation missing prefix":  {execQuery: `DROP DATABASE "prod"`, prefix: "tf_", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateAllowedPrefix(tc.execQuery, tc.preQuery, tc.prefix)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "allow-prefix") {
				t.Fatalf("error should mention allow-prefix: %v", err)
			}
		})
	}
}
