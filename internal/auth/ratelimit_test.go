package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLimiter(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewMemoryLimiter(5, 3*time.Minute)
	l.now = func() time.Time { return now }

	allow := func(key string) bool {
		ok, err := l.Allow(ctx, key)
		require.NoError(t, err)
		return ok
	}

	for range 5 {
		require.True(t, allow("1.2.3.4|a@x.io"))
		require.NoError(t, l.Fail(ctx, "1.2.3.4|a@x.io"))
	}
	assert.False(t, allow("1.2.3.4|a@x.io"), "6th attempt within the window is blocked")
	assert.True(t, allow("1.2.3.4|b@x.io"), "other email is independent")
	assert.True(t, allow("5.6.7.8|a@x.io"), "other IP is independent")

	now = now.Add(3 * time.Minute)
	assert.True(t, allow("1.2.3.4|a@x.io"), "one token refilled after 3 minutes")
	require.NoError(t, l.Fail(ctx, "1.2.3.4|a@x.io"))
	assert.False(t, allow("1.2.3.4|a@x.io"))

	now = now.Add(15 * time.Minute)
	require.NoError(t, l.Fail(ctx, "other-key")) // triggers the sweep
	assert.NotContains(t, l.buckets, "1.2.3.4|a@x.io", "fully refilled buckets are dropped")
}
