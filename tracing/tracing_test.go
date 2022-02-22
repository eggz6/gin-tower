package tracing

import "testing"

func TestNewGlobalTracer(t *testing.T) {
	tracer, closer, err := NewGlobalTracer("test_tracer")
	if err != nil {
		t.Fatalf("new trace failed. err=%v", err)
	}

	if tracer == nil {
		t.Fatalf("nil tracer ")
	}

	if closer == nil {
		t.Fatal("nil closer")
	}
}
