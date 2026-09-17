package storage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestWriterCommitsTransactionsAndRollsBackFailures(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewWriter(db, 4)
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Stop(context.Background()) }()

	if _, err := writer.Exec(context.Background(), ControlWrite, `CREATE TABLE values_table (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Tx(context.Background(), NormalWrite, nil, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO values_table (value) VALUES (?)`, "committed")
		time.Sleep(10 * time.Millisecond)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("rollback")
	if err := writer.Tx(context.Background(), NormalWrite, nil, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO values_table (value) VALUES (?)`, "rolled-back"); err != nil {
			return err
		}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("rollback error = %v, want %v", err, wantErr)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM values_table WHERE value = 'committed'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("committed rows = %d, want 1", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM values_table WHERE value = 'rolled-back'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back rows = %d, want 0", count)
	}
	stats := writer.Stats()
	if stats.Admitted != 3 || stats.Succeeded != 2 || stats.Failed != 1 || stats.Transactions != 2 {
		t.Fatalf("writer stats = %+v, want admitted=3 succeeded=2 failed=1", stats)
	}
	if stats.ByPriority["control"].Admitted != 1 || stats.ByPriority["control"].Succeeded != 1 {
		t.Fatalf("control writer stats = %+v, want admitted=1 succeeded=1", stats.ByPriority["control"])
	}
	if stats.ByPriority["normal"].Admitted != 2 || stats.ByPriority["normal"].Succeeded != 1 || stats.ByPriority["normal"].Failed != 1 || stats.ByPriority["normal"].Transactions != 2 || stats.ByPriority["normal"].DBTransactionMS < 10 || stats.ByPriority["normal"].DBSQLMS < 10 {
		t.Fatalf("normal writer stats = %+v, want admitted=2 succeeded=1 failed=1", stats.ByPriority["normal"])
	}
	if stats.ByPriority["maintenance"] != (WriterClassStats{}) {
		t.Fatalf("maintenance writer stats = %+v, want zero", stats.ByPriority["maintenance"])
	}
}

func TestWriterPrioritizesControlAfterBoundedBurst(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewWriter(db, 2)
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	appendOrder := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}
	var wg sync.WaitGroup
	queue := func(priority WritePriority, name string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writer.Do(context.Background(), priority, func(context.Context) error {
				fn()
				appendOrder(name)
				return nil
			}); err != nil {
				t.Errorf("writer %s: %v", name, err)
			}
		}()
	}

	queue(NormalWrite, "blocking", func() {
		close(started)
		<-release
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking writer did not start")
	}
	queue(NormalWrite, "normal", func() {})
	queue(MaintenanceWrite, "maintenance", func() {})
	queue(ControlWrite, "control", func() {})
	deadline := time.Now().Add(time.Second)
	for (len(writer.normal) < 1 || len(writer.maintenance) < 1 || len(writer.control) < 1) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(writer.normal) < 1 || len(writer.maintenance) < 1 || len(writer.control) < 1 {
		t.Fatal("priority queues did not receive all requests")
	}
	close(release)
	wg.Wait()
	_ = writer.Stop(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 || order[0] != "blocking" {
		t.Fatalf("writer order = %v", order)
	}
	controlIndex := -1
	lowIndex := -1
	for i, name := range order {
		if name == "control" {
			controlIndex = i
		}
		if name == "normal" || name == "maintenance" {
			if lowIndex == -1 {
				lowIndex = i
			}
		}
	}
	if controlIndex == -1 || lowIndex == -1 || controlIndex > lowIndex {
		t.Fatalf("control priority order = %v", order)
	}
	stats := writer.Stats()
	if stats.ByPriority["control"].Admitted != 1 || stats.ByPriority["normal"].Admitted != 2 || stats.ByPriority["maintenance"].Admitted != 1 {
		t.Fatalf("writer priority stats = %+v", stats.ByPriority)
	}
}

func TestWriterCancellationBeforeAdmissionIsBounded(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewWriter(db, 1)
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = writer.Do(context.Background(), NormalWrite, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking writer did not start")
	}
	// Fill the bounded queue so the canceled request cannot be admitted.
	queued := make(chan error, 1)
	go func() {
		queued <- writer.Do(context.Background(), NormalWrite, func(context.Context) error { return nil })
	}()
	deadline := time.Now().Add(time.Second)
	for len(writer.normal) < cap(writer.normal) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(writer.normal) != cap(writer.normal) {
		t.Fatal("writer queue did not become full")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = writer.Do(ctx, NormalWrite, func(context.Context) error {
		t.Fatal("canceled request executed")
		return nil
	})
	if !errors.Is(err, ErrWriterQueueFull) {
		t.Fatalf("full admission error = %v", err)
	}
	close(release)
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("queued writer did not finish")
	}
	if err := writer.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	if stats.ByPriority["normal"].Rejected != 1 {
		t.Fatalf("normal rejection stats = %+v, want rejected=1", stats.ByPriority["normal"])
	}
}
