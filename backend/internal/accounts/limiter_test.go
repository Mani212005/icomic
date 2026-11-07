package accounts_test

import (
	"testing"
	"time"

	"icomic/backend/internal/accounts"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestLimiterBlocksAfterMaxAndExpires(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	l := accounts.NewLimiter(clock.Now, 10)
	rule := accounts.Rule{Max: 3, Window: time.Minute}
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("k", rule); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
		l.Record("k", rule)
	}
	ok, wait := l.Allowed("k", rule)
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("expected a block with a retry delay, got ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allowed("other", rule); !ok {
		t.Fatal("limits are per key")
	}
	clock.now = clock.now.Add(time.Minute)
	if ok, _ := l.Allowed("k", rule); !ok {
		t.Fatal("window should have expired")
	}
}

func TestLimiterResetAndTake(t *testing.T) {
	l := accounts.NewLimiter(nil, 10)
	rule := accounts.Rule{Max: 2, Window: time.Hour}
	l.Take("k", rule)
	l.Take("k", rule)
	if ok, _ := l.Take("k", rule); ok {
		t.Fatal("third take must be refused")
	}
	l.Reset("k")
	if ok, _ := l.Take("k", rule); !ok {
		t.Fatal("reset clears the counter")
	}
}

func TestLimiterFailsClosedWhenFullOfLiveKeys(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	l := accounts.NewLimiter(clock.Now, 2)
	rule := accounts.Rule{Max: 5, Window: time.Minute}
	l.Record("a", rule)
	l.Record("b", rule)
	if ok, _ := l.Allowed("c", rule); ok {
		t.Fatal("a new key must be refused while the table is full of live keys")
	}
	clock.now = clock.now.Add(2 * time.Minute)
	if ok, _ := l.Allowed("c", rule); !ok {
		t.Fatal("expired keys are purged to make room")
	}
}
