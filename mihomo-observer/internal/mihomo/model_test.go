package mihomo

import (
	"testing"
	"time"
)

func TestDecodeFrameRejectsIncompleteAndDuplicates(t *testing.T) {
	for _, v := range []string{`{}`, `{"connections":{}}`, `{"connections":[{"id":"a"},{"id":"a"}]}`, `{"connections":[{"id":"a","metadata":{"destinationPort":"99999"}}]}`} {
		if _, e := DecodeFrame([]byte(v), time.Now()); e == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}
func TestDecodeFrameIPOnlyAndNull(t *testing.T) {
	f, e := DecodeFrame([]byte(`{"connections":[{"id":"a","metadata":{"destinationIP":"::ffff:192.0.2.1","destinationPort":"443"}}]}`), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	c := f.Connections[0]
	if c.TargetKind != "ip_only" || c.TargetValue != "192.0.2.1" || c.IPVersion != 4 {
		t.Fatalf("%+v", c)
	}
	f, e = DecodeFrame([]byte(`{"connections":null}`), time.Now())
	if e != nil || len(f.Connections) != 0 {
		t.Fatalf("%+v %v", f, e)
	}
}
