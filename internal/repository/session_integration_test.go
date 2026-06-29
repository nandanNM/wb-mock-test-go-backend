package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRotateConcurrent verifies the load-bearing property: two concurrent
// refreshes presenting the same token yield exactly one rotation (RotateOK) and
// one benign retry (RotateBenign) — never a false-positive reuse revocation.
//
// Requires a migrated database; set TEST_DATABASE_URL to run (e.g. the Neon URL).
func TestRotateConcurrent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL (a migrated DB) to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	users := NewUserRepository(pool)
	sessions := NewSessionRepository(pool)

	u, err := users.Create(ctx, "race", fmt.Sprintf("race+%d@example.com", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID) // cascades to sessions

	presented := fmt.Sprintf("presented_%d", time.Now().UnixNano())
	sid, err := sessions.Create(ctx, CreateSessionParams{
		UserID:           u.ID,
		RefreshTokenHash: presented,
		ExpiresAt:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	type out struct {
		res RotateResult
		err error
	}
	ch := make(chan out, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			res, err := sessions.Rotate(ctx, presented, fmt.Sprintf("rotated_%d", i), time.Now().Add(time.Hour))
			ch <- out{res, err}
		}(i)
	}
	a, b := <-ch, <-ch
	if a.err != nil || b.err != nil {
		t.Fatalf("rotate errors: %v / %v", a.err, b.err)
	}

	statuses := map[RotateStatus]int{a.res.Status: 0, b.res.Status: 0}
	statuses[a.res.Status]++
	statuses[b.res.Status]++
	if statuses[RotateOK] != 1 || statuses[RotateBenign] != 1 {
		t.Fatalf("expected exactly one RotateOK and one RotateBenign, got %v and %v",
			a.res.Status, b.res.Status)
	}

	// The session must NOT have been revoked by the benign concurrent refresh.
	_, revoked, err := sessions.Status(ctx, sid)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if revoked {
		t.Fatal("session was revoked by a benign concurrent refresh (false positive)")
	}

	// An unknown hash (neither current nor prev) is invalid, not reuse.
	res, err := sessions.Rotate(ctx, "totally_unknown_hash", "x", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("rotate(unknown): %v", err)
	}
	if res.Status != RotateInvalid {
		t.Fatalf("unknown token should be RotateInvalid, got %v", res.Status)
	}

	// Genuine reuse-after-grace: presenting the just-rotated-out token OUTSIDE
	// the grace window is a breach. Age rotated_at past the grace to make this
	// deterministic (no real-time wait). The winner above rotated `presented`
	// out, so its hash is now prev_refresh_token_hash.
	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET rotated_at = now() - interval '1 minute' WHERE id = $1`, sid); err != nil {
		t.Fatalf("age rotated_at: %v", err)
	}
	res, err = sessions.Rotate(ctx, presented, "y", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("rotate(reuse): %v", err)
	}
	if res.Status != RotateReuse {
		t.Fatalf("stale prev token outside grace should be RotateReuse, got %v", res.Status)
	}
}
