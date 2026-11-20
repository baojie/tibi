// Package usage counts views and bytes served, enforces daily bandwidth
// caps, and writes the counts to the database in batches (PLAN A8.10).
package usage

import (
	"context"
	"sync"
	"time"

	"github.com/baojie/tibi/internal/store"
)

// Tracker accumulates traffic in memory. Caps are checked against today's
// in-memory totals, so they act immediately; Flush persists them.
type Tracker struct {
	st  *store.Store
	now func() time.Time

	mu       sync.Mutex
	day      string
	itemDay  map[string]int64 // bytes per item today (persisted + pending)
	totalDay int64
	pending  map[string]*store.UsageBatch // per day, so midnight splits cleanly
}

// New returns a tracker; call Load before serving.
func New(st *store.Store) *Tracker {
	return &Tracker{st: st, now: func() time.Time { return st.Now() }}
}

// Day returns the UTC day key for t.
func Day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// batch returns the pending batch for today.
func (t *Tracker) batch() *store.UsageBatch {
	b, ok := t.pending[t.day]
	if !ok {
		b = &store.UsageBatch{Day: t.day, Bytes: map[string]int64{}, Views: map[string]int64{}}
		t.pending[t.day] = b
	}
	return b
}

// Load reads today's totals from the database.
func (t *Tracker) Load(ctx context.Context) error {
	day := Day(t.now())
	per, total, err := t.st.DailyUsage(ctx, day)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.day, t.itemDay, t.totalDay = day, per, total
	t.pending = map[string]*store.UsageBatch{}
	return nil
}

// rollover starts a new day when the date changed. Pending counts of the
// old day stay in their own batch until the next Flush.
func (t *Tracker) rollover() {
	if d := Day(t.now()); d != t.day {
		t.day, t.itemDay, t.totalDay = d, map[string]int64{}, 0
	}
}

// Over reports whether serving more of item id today would break a cap
// (0 means unlimited): the item's own, or the instance total.
func (t *Tracker) Over(id string, itemCap, totalCap int64) (itemOver, totalOver bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover()
	return itemCap > 0 && t.itemDay[id] >= itemCap, totalCap > 0 && t.totalDay >= totalCap
}

// Reserve checks the caps like Over and, if serving may go ahead, counts n
// bytes against today's totals at once, so concurrent requests see them
// (counting only after a response finished let parallel downloads all pass
// a nearly used-up cap). release undoes the reservation; the bytes actually
// sent are then recorded with Add.
func (t *Tracker) Reserve(id string, n, itemCap, totalCap int64) (itemOver, totalOver bool, release func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover()
	itemOver, totalOver = itemCap > 0 && t.itemDay[id] >= itemCap, totalCap > 0 && t.totalDay >= totalCap
	if itemOver || totalOver || n <= 0 {
		return itemOver, totalOver, func() {}
	}
	day := t.day
	t.itemDay[id] += n
	t.totalDay += n
	var once sync.Once
	return false, false, func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.day == day { // a new day started from zero anyway
				t.itemDay[id] -= n
				t.totalDay -= n
			}
		})
	}
}

// Add records a hit (view) and/or bytes served for item id.
func (t *Tracker) Add(id string, bytes int64, view bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover()
	b := t.batch()
	if view {
		b.Views[id]++
	}
	if bytes > 0 {
		b.Bytes[id] += bytes
		b.Total += bytes
		t.itemDay[id] += bytes
		t.totalDay += bytes
	}
}

// Today returns bytes served today for an item and in total.
func (t *Tracker) Today(id string) (item, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover()
	return t.itemDay[id], t.totalDay
}

// Flush writes pending counts to the database.
func (t *Tracker) Flush(ctx context.Context) error {
	t.mu.Lock()
	batches := t.pending
	t.pending = map[string]*store.UsageBatch{}
	t.mu.Unlock()
	var firstErr error
	for day, b := range batches {
		if err := t.st.AddUsage(ctx, *b); err != nil {
			// Put the counts back so they are retried.
			t.mu.Lock()
			if cur, ok := t.pending[day]; ok {
				for id, n := range b.Bytes {
					cur.Bytes[id] += n
				}
				for id, n := range b.Views {
					cur.Views[id] += n
				}
				cur.Total += b.Total
			} else {
				t.pending[day] = b
			}
			t.mu.Unlock()
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Loop flushes every interval until ctx ends, then once more.
func (t *Tracker) Loop(ctx context.Context, interval time.Duration, onErr func(error)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := t.Flush(context.Background()); err != nil && onErr != nil {
				onErr(err)
			}
			return
		case <-tick.C:
			if err := t.Flush(ctx); err != nil && onErr != nil {
				onErr(err)
			}
			// Keep 400 days of daily rows for stats.
			t.st.PruneUsage(ctx, Day(t.now().AddDate(0, 0, -400)))
		}
	}
}
