package main

import (
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/model"
)

type scriptedResolver struct {
	calls atomic.Int32
	// after is the call number from which Resolve returns bs instead of nil.
	after int32
	bs    []model.Binding
	err   error
}

func (r *scriptedResolver) Resolve(model.Filter) ([]model.Binding, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.calls.Add(1) > r.after {
		return r.bs, nil
	}
	return nil, nil
}

func (r *scriptedResolver) ListAll() ([]model.Binding, error) { return nil, nil }

func binding(s model.SocketState) model.Binding {
	return model.Binding{Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"), State: s}
}

func TestReached(t *testing.T) {
	tests := []struct {
		name string
		bs   []model.Binding
		busy bool
		want bool
	}{
		{"free and empty", nil, false, true},
		{"free but listening", []model.Binding{binding(model.StateListen)}, false, false},
		{"free but time-wait", []model.Binding{binding(model.StateTimeWait)}, false, false},
		{"busy and empty", nil, true, false},
		{"busy and listening", []model.Binding{binding(model.StateListen)}, true, true},
		{"busy but only time-wait", []model.Binding{binding(model.StateTimeWait)}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reached(tc.bs, tc.busy); got != tc.want {
				t.Fatalf("reached(%v, busy=%v) = %v, want %v", tc.bs, tc.busy, got, tc.want)
			}
		})
	}
}

func TestWaitForFreeReturnsImmediately(t *testing.T) {
	r := &scriptedResolver{after: 100}
	ok, err := waitFor(r, model.Filter{Port: 8080}, false, time.Second, time.Millisecond)
	if err != nil || !ok || r.calls.Load() != 1 {
		t.Fatalf("got ok=%v err=%v calls=%d, want ok after one call", ok, err, r.calls.Load())
	}
}

func TestWaitForBusyPollsUntilPortIsTaken(t *testing.T) {
	r := &scriptedResolver{after: 3, bs: []model.Binding{binding(model.StateListen)}}
	ok, err := waitFor(r, model.Filter{Port: 8080}, true, 5*time.Second, time.Millisecond)
	if err != nil || !ok || r.calls.Load() != 4 {
		t.Fatalf("got ok=%v err=%v calls=%d, want ok on the 4th call", ok, err, r.calls.Load())
	}
}

func TestWaitForTimesOut(t *testing.T) {
	r := &scriptedResolver{bs: []model.Binding{binding(model.StateListen)}}
	ok, err := waitFor(r, model.Filter{Port: 8080}, false, 30*time.Millisecond, time.Millisecond)
	if err != nil || ok {
		t.Fatalf("got ok=%v err=%v, want a timeout", ok, err)
	}
}

func TestWaitForZeroTimeoutWaitsForever(t *testing.T) {
	r := &scriptedResolver{after: 20, bs: []model.Binding{binding(model.StateListen)}}
	ok, err := waitFor(r, model.Filter{Port: 8080}, true, 0, time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("got ok=%v err=%v, want success without a deadline", ok, err)
	}
}

func TestWaitForReturnsDiscoveryError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := waitFor(&scriptedResolver{err: boom}, model.Filter{Port: 8080}, false, time.Second, time.Millisecond); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestRunWaitRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"8080", "9090"},
		{"--free", "--busy", "8080"},
		{"--timeout", "-1s", "8080"},
		{"--timeout", "soon", "8080"},
		{"--protocol", "sctp", "8080"},
		{"--nope", "8080"},
		{"notaport"},
	} {
		if code := runWait(args); code != exitFailure {
			t.Errorf("runWait(%q) = %d, want %d", args, code, exitFailure)
		}
	}
}
