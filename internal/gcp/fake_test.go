package gcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFakeLicensing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.json")
	f, err := NewFakeLicensing(path, 2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	orders, _ := f.ListOrders(ctx)
	if len(orders) != 1 || !orders[0].ActiveAt(time.Now()) {
		t.Fatalf("orders = %+v", orders)
	}
	order := orders[0].Name
	if err := f.Assign(ctx, order, []string{"a@example.com", "b@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Assign(ctx, order, []string{"a@example.com"}); err != nil {
		t.Fatal("re-assigning a licensed user must be idempotent")
	}
	if err := f.Assign(ctx, order, []string{"c@example.com"}); err == nil {
		t.Fatal("assignment beyond the pool size accepted")
	}
	pool, _ := f.GetPool(ctx, order)
	if pool.Total != 2 || pool.Available != 0 || pool.Assigned() != 2 {
		t.Fatalf("pool = %+v", pool)
	}

	// State survives a restart.
	f2, err := NewFakeLicensing(path, 99, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if users, _ := f2.Enumerate(ctx, order); len(users) != 2 {
		t.Fatalf("persisted users = %d", len(users))
	}

	// Rollover: the old order ends, the new one is empty.
	next, err := f2.Rollover(5)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := f2.GetPool(ctx, order); p.Total != 0 {
		t.Fatalf("old pool still active: %+v", p)
	}
	if p, _ := f2.GetPool(ctx, next); p.Total != 5 || p.Available != 5 {
		t.Fatalf("new pool = %+v", p)
	}
}

func TestFakeSlowMarker(t *testing.T) {
	ctx := context.Background()
	f, _ := NewFakeLicensing("", 2, 0, 0)
	orders, _ := f.ListOrders(ctx)
	tctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err := f.Assign(tctx, orders[0].Name, []string{"nina.slow@example.com"}); err == nil {
		t.Fatal("first assign of a 'slow' user must hang until the deadline")
	}
	if err := f.Assign(ctx, orders[0].Name, []string{"nina.slow@example.com"}); err != nil {
		t.Fatalf("second assign: %v", err)
	}
}
