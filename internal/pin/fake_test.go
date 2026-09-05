package pin

import (
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestFakeControllerRecordsCalls(t *testing.T) {
	f := NewFake(model.ProcMeta{PID: 7, Name: "worker"})

	if f.Meta().PID != 7 {
		t.Fatalf("Meta().PID = %d, want 7", f.Meta().PID)
	}
	if err := f.Graceful(); err != nil {
		t.Fatal(err)
	}
	if err := f.Hard(); err != nil {
		t.Fatal(err)
	}
	if got := f.Calls(); len(got) != 2 || got[0] != "graceful" || got[1] != "hard" {
		t.Fatalf("Calls() = %v, want [graceful hard]", got)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !f.Closed() {
		t.Error("Closed() = false after Close()")
	}
}

func TestFakeControllerScriptedErrors(t *testing.T) {
	f := NewFake(model.ProcMeta{PID: 7})
	f.GracefulErr = ErrNoConsole
	if err := f.Graceful(); err != ErrNoConsole {
		t.Fatalf("Graceful() = %v, want ErrNoConsole", err)
	}

	f.LifecycleSeq = []model.Lifecycle{model.Alive, model.Zombie, model.Gone}
	for _, want := range []model.Lifecycle{model.Alive, model.Zombie, model.Gone, model.Gone} {
		got, err := f.Lifecycle()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Lifecycle() = %v, want %v", got, want)
		}
	}
}
