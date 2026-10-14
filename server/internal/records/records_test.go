package records

import (
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestBodyReturnsZeroOnDecodeFailure(t *testing.T) {
	type evidence struct {
		FullText  bool `json:"full_text"`
		Retracted bool `json:"retracted"`
	}
	for _, tc := range []struct {
		name, body string
		want       evidence
	}{
		{"valid", `{"full_text":true,"retracted":false}`, evidence{FullText: true}},
		{"partial type error", `{"full_text":true,"retracted":"bad"}`, evidence{}},
		{"invalid JSON", `{"full_text":true,`, evidence{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Body[evidence](db.Record{Body: []byte(tc.body)}); got != tc.want {
				t.Errorf("decoded evidence = %+v, want %+v", got, tc.want)
			}
		})
	}
}
