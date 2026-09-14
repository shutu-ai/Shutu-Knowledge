package knowledge

import (
	"context"
	"testing"
	"time"
)

func TestStatsCacheInvalidatesAndExpires(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "First", "cache invalidation body"); err != nil {
		t.Fatal(err)
	}
	first, err := f.service.Stats(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.DocumentCount != 1 || first.ChunkCount < 1 {
		t.Fatalf("initial stats: %+v", first)
	}

	// A service mutation invalidates the cached aggregate even before the TTL
	// expires, so overview pages do not wait for stale counts to age out.
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "Second", "another bounded body"); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Stats(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.DocumentCount != 2 {
		t.Fatalf("mutation did not invalidate stats: %+v", second)
	}

	// Advancing the clock proves the short TTL is active for direct SQL
	// maintenance paths that bypass service mutation wrappers.
	f.service.store.statsNow = func() time.Time {
		return time.Now().Add(2 * time.Second)
	}
	if _, err := f.service.store.db.Exec(
		`UPDATE documents SET chunk_count = 99 WHERE base_id = ?`, base.ID,
	); err != nil {
		t.Fatal(err)
	}
	third, err := f.service.Stats(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if third.ChunkCount != 198 {
		t.Fatalf("ttl did not refresh aggregate: %+v", third)
	}
	bases, err := f.service.ListBases()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range bases {
		if item.ID == base.ID && (item.DocumentCount != 2 || item.ChunkCount != 198) {
			t.Fatalf("base summary did not use refreshed stats: %+v", item)
		}
	}
}
