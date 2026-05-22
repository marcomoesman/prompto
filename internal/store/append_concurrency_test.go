package store

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// TestAppendMessage_ConcurrentOrdinalsAreUniqueAndContiguous regresses
// the SELECT MAX → INSERT TOCTOU window. The fixed implementation
// uses a single INSERT…SELECT statement, which SQLite serializes at
// the page-write level — N goroutines racing on AppendMessage must
// produce ordinals 0..N-1 with no gaps and no UNIQUE-constraint
// violations.
func TestAppendMessage_ConcurrentOrdinalsAreUniqueAndContiguous(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(OpenInput{Path: filepath.Join(dir, "db.sqlite")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Close the store before TempDir's cleanup runs. On Windows an
	// open SQLite handle keeps the file locked; the tempdir RemoveAll
	// would otherwise fail with "being used by another process".
	t.Cleanup(func() { _ = s.Close() })

	sess, err := s.CreateSession(context.Background(), CreateSessionInput{Model: "m"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	const N = 32
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			msg := api.NewUserMessage("hi")
			if err := s.AppendMessage(context.Background(), sess.ID, msg, nil); err != nil {
				errsMu.Lock()
				errs = append(errs, err)
				errsMu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("AppendMessage errors under concurrency: %v", errs)
	}

	msgs, err := s.LoadMessages(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgs) != N {
		t.Fatalf("expected %d messages persisted, got %d", N, len(msgs))
	}

	// Ordinals aren't returned through the public API, so we read them
	// directly. They must form the closed range [0, N-1] exactly.
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT ordinal FROM messages WHERE session_id = ? ORDER BY ordinal`,
		sess.ID)
	if err != nil {
		t.Fatalf("query ordinals: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []int
	for rows.Next() {
		var ord int
		if err := rows.Scan(&ord); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, ord)
	}
	sort.Ints(got)
	for i, ord := range got {
		if ord != i {
			t.Errorf("ordinal[%d] = %d; expected contiguous [0, N-1] — gap or duplicate", i, ord)
		}
	}
}
