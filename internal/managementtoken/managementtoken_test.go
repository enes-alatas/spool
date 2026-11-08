package managementtoken

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// tokenStore holds tokens by value, as the sqlite store holds them by hash.
type tokenStore struct {
	store.ManagementTokenStore
	byValue map[string]*store.ManagementToken
	touched []int64
}

func (tokens *tokenStore) GetByToken(_ context.Context, token string) (*store.ManagementToken, error) {
	if managementToken, ok := tokens.byValue[token]; ok {
		return managementToken, nil
	}
	return nil, store.ErrNotFound
}

func (tokens *tokenStore) Touch(_ context.Context, id string, at int64) error {
	for _, managementToken := range tokens.byValue {
		if managementToken.ID == id {
			managementToken.LastUsedAt = at
		}
	}
	tokens.touched = append(tokens.touched, at)
	return nil
}

// Verify takes a held token before its end, and refuses everything else
// alike: no shape, the operator token's or a loop token's shape, a token
// never minted or revoked, and one at or past its end (ADR-0067).
func TestVerifyTakesOnlyAHeldTokenBeforeItsEnd(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	live, ending, endless := store.NewManagementToken(), store.NewManagementToken(), store.NewManagementToken()
	tokens := &tokenStore{byValue: map[string]*store.ManagementToken{
		live:    {ID: "manage_1", Name: "watcher", ExpiresAt: now.UnixMilli() + 1},
		ending:  {ID: "manage_2", Name: "ended", ExpiresAt: now.UnixMilli()},
		endless: {ID: "manage_3", Name: "endless"},
	}}
	ctx := context.Background()
	checker := &Checker{Tokens: tokens, Now: func() time.Time { return now }}
	for _, token := range []string{live, endless} {
		got, err := checker.Verify(ctx, token)
		if want := tokens.byValue[token]; err != nil || got != (Identity{ID: want.ID, Name: want.Name}) {
			t.Fatalf("Verify(%s's token) = %+v, %v", tokens.byValue[token].Name, got, err)
		}
	}
	if len(tokens.touched) != 2 || tokens.touched[0] != now.UnixMilli() {
		t.Fatalf("uses recorded %v, want two at %d", tokens.touched, now.UnixMilli())
	}
	refused := map[string]string{
		"past its end":   ending,
		"never minted":   store.NewManagementToken(),
		"empty":          "",
		"a loop token's": store.NewHubMCPToken(),
		"the prefix":     store.ManagementTokenPrefix,
		"padded":         live + "x",
		"in a sentence":  "token " + live,
	}
	for name, token := range refused {
		if got, err := checker.Verify(ctx, token); !errors.Is(err, ErrRefused) || got != (Identity{}) {
			t.Errorf("Verify(%s) = %+v, %v; want ErrRefused", name, got, err)
		}
	}
	if len(tokens.touched) != 2 {
		t.Fatalf("a refused token recorded a use: %v", tokens.touched)
	}
	if actor := (Identity{ID: "manage_1", Name: "watcher"}).Actor(); actor != "manage:watcher#manage_1" {
		t.Fatalf("Actor = %q", actor)
	}
}

// A token's calls within a minute of its last recorded use write nothing:
// Verify leaves Touch out, so they don't each take the database's write
// lock (ADR-0067).
func TestVerifyWritesAUseOnceAMinute(t *testing.T) {
	minted := store.NewManagementToken()
	tokens := &tokenStore{byValue: map[string]*store.ManagementToken{minted: {ID: "manage_1", Name: "watcher"}}}
	clock := time.UnixMilli(1_000_000)
	checker := &Checker{Tokens: tokens, Now: func() time.Time { return clock }}
	for _, step := range []struct {
		after   time.Duration
		touched int
	}{{0, 1}, {30 * time.Second, 1}, {59 * time.Second, 1}, {60 * time.Second, 2}, {61 * time.Second, 2}} {
		clock = time.UnixMilli(1_000_000).Add(step.after)
		if _, err := checker.Verify(context.Background(), minted); err != nil {
			t.Fatal(err)
		}
		if len(tokens.touched) != step.touched {
			t.Fatalf("a call %v on: %d uses written, want %d", step.after, len(tokens.touched), step.touched)
		}
	}
}
