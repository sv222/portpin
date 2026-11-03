package pin

import (
	"errors"
	"slices"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

type logged struct {
	*FakeController
	log *[]uint32
}

func (l logged) Graceful() error {
	*l.log = append(*l.log, l.Meta().PID)
	return l.FakeController.Graceful()
}

func TestPinGroupSignalsMembersInOrder(t *testing.T) {
	var log []uint32
	primary := logged{NewFake(model.ProcMeta{PID: 22}), &log}
	fakes := map[uint32]*FakeController{}
	pinFn := func(m model.ProcMeta) (Controller, error) {
		switch m.PID {
		case 23:
			return nil, ErrProcessGone
		case 24:
			return nil, ErrIdentityChanged
		}
		f := NewFake(m)
		fakes[m.PID] = f
		return logged{f, &log}, nil
	}
	metas := []model.ProcMeta{{PID: 20}, {PID: 21}, {PID: 22}, {PID: 23}, {PID: 24}}
	c, pids, err := PinGroup(primary, metas, pinFn)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pids, []uint32{20, 21, 22}) {
		t.Fatalf("pids = %v", pids)
	}
	if err := c.Graceful(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(log, []uint32{20, 21, 22}) {
		t.Fatalf("signal order = %v", log)
	}
	if err := c.Hard(); err != nil {
		t.Fatal(err)
	}
	if c.Meta().PID != 22 {
		t.Errorf("meta pid = %d", c.Meta().PID)
	}
	_ = c.Close()
	if !fakes[20].Closed() || !fakes[21].Closed() || !primary.Closed() {
		t.Error("not every member was closed")
	}
}

func TestPinGroupFailsClosedOnPermission(t *testing.T) {
	primary := NewFake(model.ProcMeta{PID: 2})
	first := NewFake(model.ProcMeta{PID: 1})
	pinFn := func(m model.ProcMeta) (Controller, error) {
		if m.PID == 1 {
			return first, nil
		}
		return nil, ErrPermission
	}
	_, _, err := PinGroup(primary, []model.ProcMeta{{PID: 1}, {PID: 2}, {PID: 3}}, pinFn)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("err = %v, want ErrPermission", err)
	}
	if !first.Closed() || primary.Closed() {
		t.Error("pinned members must be released and the primary left to the caller")
	}
}

func TestGroupErrorPrecedence(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name           string
		primary, other error
		want           error
	}{
		{"all ok", nil, nil, nil},
		{"member gone", nil, ErrProcessGone, nil},
		{"member no console", nil, ErrNoConsole, nil},
		{"member failure", nil, boom, boom},
		{"primary gone, member failure", ErrProcessGone, boom, boom},
		{"primary gone", ErrProcessGone, nil, ErrProcessGone},
		{"primary identity wins", ErrIdentityChanged, boom, ErrIdentityChanged},
		{"primary no console", ErrNoConsole, nil, ErrNoConsole},
	}
	for _, c := range cases {
		p := NewFake(model.ProcMeta{PID: 2})
		p.GracefulErr = c.primary
		o := NewFake(model.ProcMeta{PID: 1})
		o.GracefulErr = c.other
		g, _, _ := PinGroup(p, []model.ProcMeta{{PID: 1}}, func(model.ProcMeta) (Controller, error) { return o, nil })
		if err := g.Graceful(); !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if got := o.Calls(); !slices.Equal(got, []string{"graceful"}) {
			t.Errorf("%s: member calls = %v", c.name, got)
		}
	}
}
