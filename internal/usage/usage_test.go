package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tibi/internal/store"
)

func TestTracker(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	clock := time.Date(2026, 11, 20, 23, 59, 0, 0, time.UTC)
	st.Now = func() time.Time { return clock }
	_, tok, _ := st.CreateToken(ctx, "t", []string{"upload"}, nil)
	it := &store.Item{Kind: "file", Mime: "x", Ext: "bin", Size: 1, SHA256: "00", TokenID: tok.ID}
	st.CreateItem(ctx, it)

	tr := New(st)
	tr.Load(ctx)
	tr.Add(it.ID, 600, true)
	if io, _ := tr.Over(it.ID, 1000, 0); io {
		t.Fatal("over cap too early")
	}
	tr.Add(it.ID, 500, false)
	if io, to := tr.Over(it.ID, 1000, 2000); !io || to {
		t.Fatalf("caps: item %v total %v", io, to)
	}
	// Midnight: caps reset; the old day's counts are still flushed to it.
	clock = clock.Add(2 * time.Minute)
	if io, _ := tr.Over(it.ID, 1000, 0); io {
		t.Fatal("cap not reset at midnight")
	}
	tr.Add(it.ID, 70, true)
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	per, total, _ := st.DailyUsage(ctx, "2026-11-20")
	if per[it.ID] != 1100 || total != 1100 {
		t.Fatalf("yesterday: %v %d", per, total)
	}
	per, total, _ = st.DailyUsage(ctx, "2026-11-21")
	if per[it.ID] != 70 || total != 70 {
		t.Fatalf("today: %v %d", per, total)
	}
	got, _ := st.GetItem(ctx, it.ID)
	if got.ViewCount != 2 {
		t.Fatalf("views = %d", got.ViewCount)
	}
	// A restart reloads today's totals.
	tr2 := New(st)
	tr2.Load(ctx)
	if n, total := tr2.Today(it.ID); n != 70 || total != 70 {
		t.Fatalf("reloaded: %d %d", n, total)
	}
}
