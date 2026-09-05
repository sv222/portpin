package pin

import (
	"sync"

	"github.com/sv222/portpin/internal/model"
)

// FakeController is a scripted Controller for tests in this and other
// packages. It lives in a non-test file so internal/terminate can import it.
type FakeController struct {
	meta model.ProcMeta

	// GracefulErr and HardErr are returned by the matching method when set.
	GracefulErr error
	HardErr     error

	// LifecycleSeq is consumed one entry per Lifecycle call. When it runs out,
	// the last entry repeats. An empty sequence always reports Alive.
	LifecycleSeq []model.Lifecycle
	LifecycleErr error

	mu     sync.Mutex
	calls  []string
	closed bool
	seqIdx int
}

// NewFake returns a FakeController that succeeds at everything and always
// reports the process as alive.
func NewFake(meta model.ProcMeta) *FakeController {
	return &FakeController{meta: meta}
}

func (f *FakeController) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

func (f *FakeController) Graceful() error {
	f.record("graceful")
	return f.GracefulErr
}

func (f *FakeController) Hard() error {
	f.record("hard")
	return f.HardErr
}

func (f *FakeController) Lifecycle() (model.Lifecycle, error) {
	if f.LifecycleErr != nil {
		return model.Alive, f.LifecycleErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.LifecycleSeq) == 0 {
		return model.Alive, nil
	}
	i := f.seqIdx
	if i >= len(f.LifecycleSeq) {
		i = len(f.LifecycleSeq) - 1
	} else {
		f.seqIdx++
	}
	return f.LifecycleSeq[i], nil
}

func (f *FakeController) Meta() model.ProcMeta { return f.meta }

func (f *FakeController) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// Calls returns the ordered names of the mutating methods invoked so far.
func (f *FakeController) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Closed reports whether Close has been called.
func (f *FakeController) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// compile-time check
var _ Controller = (*FakeController)(nil)
