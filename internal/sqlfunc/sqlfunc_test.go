package sqlfunc

import (
	"context"
	"errors"
	"testing"
)

type countingExister struct {
	calls     int
	available bool
	err       error
}

func (c *countingExister) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.calls++
	if query != probeQuery || len(args) != 1 {
		return false, errors.New("unexpected probe")
	}
	return c.available, c.err
}

func TestExistsCachesWithinOperation(t *testing.T) {
	client := &countingExister{available: true}
	ctx := WithCache(t.Context())
	for range 3 {
		available, err := Exists(ctx, client, "MD_GET_FLIGHT")
		if err != nil || !available {
			t.Fatalf("Exists() = %v, %v", available, err)
		}
	}
	if _, err := Exists(ctx, client, "md_get_flight"); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatalf("probe calls = %d, want 1 for one function within one operation", client.calls)
	}
	if WithCache(ctx) != ctx {
		t.Fatal("nested WithCache must reuse the operation cache")
	}
}

func TestExistsWithoutCacheProbesEachTime(t *testing.T) {
	client := &countingExister{}
	for range 2 {
		if available, err := Exists(t.Context(), client, "md_get_flight"); err != nil || available {
			t.Fatalf("Exists() = %v, %v", available, err)
		}
	}
	if client.calls != 2 {
		t.Fatalf("probe calls = %d, want 2 without a cache", client.calls)
	}
}

func TestExistsDoesNotCacheErrors(t *testing.T) {
	client := &countingExister{err: errors.New("permission denied")}
	ctx := WithCache(t.Context())
	if _, err := Exists(ctx, client, "md_get_flight"); err == nil {
		t.Fatal("expected probe error")
	}
	client.err = nil
	client.available = true
	if available, err := Exists(ctx, client, "md_get_flight"); err != nil || !available {
		t.Fatalf("Exists() after error = %v, %v", available, err)
	}
}

type parameterExister struct {
	calls int
	args  []any
}

func (c *parameterExister) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.calls++
	if query != parameterProbeQuery {
		return false, errors.New("unexpected probe")
	}
	c.args = args
	return true, nil
}

func TestParameterExistsProbesFunctionAndParameter(t *testing.T) {
	client := &parameterExister{}
	ctx := WithCache(t.Context())
	for range 2 {
		available, err := ParameterExists(ctx, client, "MD_SET_GUIDE_ACCESS", "role_names")
		if err != nil || !available {
			t.Fatalf("ParameterExists() = %v, %v", available, err)
		}
	}
	if client.calls != 1 {
		t.Fatalf("probe calls = %d, want 1 within one operation", client.calls)
	}
	if len(client.args) != 2 || client.args[0] != "MD_SET_GUIDE_ACCESS" || client.args[1] != "role_names" {
		t.Fatalf("probe args = %v", client.args)
	}
	counting := &countingExister{available: true}
	if _, err := Exists(ctx, counting, "md_set_guide_access"); err != nil || counting.calls != 1 {
		t.Fatalf("a function probe must not reuse a parameter probe result: calls=%d err=%v", counting.calls, err)
	}
}
