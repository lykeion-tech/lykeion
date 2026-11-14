package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	R *redis.Client
}

// Allow counts calls per key in a fixed window. A Redis error is returned to
// the caller, which refuses the request: sign-in limits fail closed.
func (l Limiter) Allow(ctx context.Context, key string, n int, window time.Duration) (bool, error) {
	if l.R == nil {
		return false, fmt.Errorf("rate limiter is unavailable")
	}
	if n <= 0 || window.Milliseconds() <= 0 {
		return false, fmt.Errorf("invalid rate limit")
	}
	count, err := limitScript.Run(ctx, l.R, []string{"lyk:rl:" + key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return count <= int64(n), nil
}

// Increment and window creation must be inseparable, including during crashes.
var limitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return count
`)
