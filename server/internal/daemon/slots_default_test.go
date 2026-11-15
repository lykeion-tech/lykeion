package daemon

import "testing"

// A daemon runs as many agent runs at once as Multica's daemon does by
// default, unless told otherwise.
func TestDefaultSlots(t *testing.T) {
	var c Config
	c.defaults()
	if c.Slots != 20 || DefaultSlots != 20 {
		t.Fatalf("default slots %d (DefaultSlots %d), want 20", c.Slots, DefaultSlots)
	}
	c = Config{Slots: 3}
	c.defaults()
	if c.Slots != 3 {
		t.Fatalf("a chosen slot count was overridden: %d", c.Slots)
	}
}
