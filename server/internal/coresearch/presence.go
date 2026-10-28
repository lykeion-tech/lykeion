package coresearch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	db "lykeion/server/pkg/db/generated"
)

type Cursor struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	Anchor int       `json:"anchor"`
	Head   int       `json:"head"`
}
type Presence struct{ Redis *redis.Client }

func (p Presence) Put(ctx context.Context, ws, file uuid.UUID, c Cursor) error {
	if p.Redis == nil {
		return nil
	}
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	if e = p.Redis.Set(ctx, "lyk:presence:"+file.String()+":"+c.UserID.String(), b, 10*time.Second).Err(); e != nil {
		return e
	}
	return p.Redis.Publish(ctx, "lyk:ws:"+ws.String(), "presence").Err()
}
func (p Presence) List(ctx context.Context, file uuid.UUID, members []db.CoresearchPresenceMembersRow) ([]Cursor, error) {
	out := []Cursor{}
	if p.Redis == nil || len(members) == 0 {
		return out, nil
	}
	keys := make([]string, len(members))
	for i, m := range members {
		keys[i] = "lyk:presence:" + file.String() + ":" + m.UserID.String()
	}
	values, e := p.Redis.MGet(ctx, keys...).Result()
	if e != nil {
		return nil, e
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		var c Cursor
		s, ok := value.(string)
		if ok && json.Unmarshal([]byte(s), &c) == nil && c.UserID == members[i].UserID {
			c.Name = members[i].Name
			out = append(out, c)
		}
	}
	return out, nil
}
