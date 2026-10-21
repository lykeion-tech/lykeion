package storageinfo

import (
	"encoding/json"
	"testing"
)

func TestMeasurementsRequireExplicitIntegers(t *testing.T) {
	for _, raw := range []string{
		`{"key":"state","path":"/state","complete":true}`,
		`{"key":"state","path":"/state","bytes":null,"complete":true}`,
		`{"key":"state","path":"/state","bytes":1.2,"complete":true}`,
		`{"key":"state","path":"/state","bytes":0,"complete":null}`,
		`{"key":"state","path":"/state","bytes":0,"complete":true,"unknown":1}`,
	} {
		var c Category
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatalf("accepted malformed measurement %s", raw)
		}
	}
	var c Category
	if err := json.Unmarshal([]byte(`{"key":"state","path":"/state","bytes":0,"complete":true}`), &c); err != nil || c.Bytes != 0 || !c.Complete {
		t.Fatalf("explicit zero rejected: %+v %v", c, err)
	}
	for _, raw := range []string{`{"id":"1","available_bytes":null,"paths":["/state"]}`, `{"id":"1","paths":["/state"]}`} {
		var f Filesystem
		if json.Unmarshal([]byte(raw), &f) == nil {
			t.Fatalf("accepted unknown capacity %s", raw)
		}
	}
}
