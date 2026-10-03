package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// TestDictionaryAttachSurvivesPooling is the test the attaching connector
// exists for.
//
// ATTACH is per-connection state and database/sql owns the connections, so an
// attach run once after sql.Open covers only whichever connection happened to
// serve it. The test proves three things in order, because the first two are
// what make the third mean anything: that a pool under load really does open
// more than one connection, that a one-shot attach really does fail on the
// others, and that the connector's hook does not.
func TestDictionaryAttachSurvivesPooling(t *testing.T) {
	dictPath := miniDict(t)
	dir := filepath.Dir(dictPath)

	hooked := sql.OpenDB(attachingConnector(filepath.Join(dir, "hooked.db"), dictPath))
	defer hooked.Close()
	hooked.SetMaxOpenConns(4)

	if failures := hammer(hooked); failures != 0 {
		t.Errorf("%d of %d queries failed through the hooked connector", failures, hammerQueries)
	}
	opened := hooked.Stats().OpenConnections
	t.Logf("the hooked connector served %d queries from %d connections", hammerQueries, opened)
	if opened < 2 {
		t.Fatal("the pool only ever opened one connection, so this test proves nothing")
	}

	// The same load against the approach the connector replaced.
	plain, err := sql.Open("sqlite", filepath.Join(dir, "plain.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer plain.Close()
	plain.SetMaxOpenConns(4)

	if _, err := plain.Exec(`ATTACH DATABASE ? AS `+DictAlias, dictPath); err != nil {
		t.Fatalf("one-shot attach: %v", err)
	}
	failures := hammer(plain)
	t.Logf("a one-shot attach failed %d of %d queries", failures, hammerQueries)
	if failures == 0 {
		t.Error("a one-shot attach survived a pooled workload, so the hook is buying nothing")
	}
}

const hammerQueries = 16

// miniDict writes a dictionary holding a single item and returns its path.
func miniDict(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dict.db")
	dict, err := OpenDict(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenDict: %v", err)
	}
	exec(t, dict.DB(), `
		INSERT INTO item (key, kind, ref_id, prompt, answers, normalized, hint, gloss, sort_key)
		VALUES ('k:日', 'kanji', 26085, '日', 'にち', 'にち', 'ニチ', 'day', 1)`)
	if err := dict.Close(); err != nil {
		t.Fatalf("close dict: %v", err)
	}
	return path
}

// hammer runs concurrent dictionary queries and returns how many failed.
func hammer(db *sql.DB) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0

	for range hammerQueries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM ` + DictAlias + `.item`).Scan(&n); err != nil || n != 1 {
				mu.Lock()
				failures++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failures
}
