package providerctx

import (
	"context"
	"errors"
	"testing"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func TestSQLClientRetriesAfterCanceledInitialization(t *testing.T) {
	providerContext := &Context{SQLConfig: mdsql.Config{Token: "dummy"}}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := providerContext.SQLClient(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first SQLClient error = %v, want context.Canceled", err)
	}

	client, err := providerContext.SQLClient(context.Background())
	if err != nil {
		t.Fatalf("retrying SQLClient after canceled initialization: %v", err)
	}
	if client == nil || !client.Available() {
		t.Fatal("retrying SQLClient should return an available client")
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("closing client: %v", err)
		}
	})
}
