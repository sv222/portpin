package pin

import (
	"errors"

	"github.com/sv222/portpin/internal/model"
)

type groupController struct {
	primary Controller
	members []Controller
}

func PinGroup(primary Controller, metas []model.ProcMeta, pinFn func(model.ProcMeta) (Controller, error)) (Controller, []uint32, error) {
	g := &groupController{primary: primary}
	pids := make([]uint32, 0, len(metas))
	hasPrimary := false
	for _, m := range metas {
		c := primary
		if m.PID == primary.Meta().PID {
			hasPrimary = true
		} else {
			var err error
			c, err = pinFn(m)
			if errors.Is(err, ErrProcessGone) || errors.Is(err, ErrIdentityChanged) {
				continue
			}
			if err != nil {
				for _, x := range g.members {
					if x != primary {
						_ = x.Close()
					}
				}
				return nil, nil, err
			}
		}
		g.members = append(g.members, c)
		pids = append(pids, m.PID)
	}
	if !hasPrimary {
		g.members = append(g.members, primary)
		pids = append(pids, primary.Meta().PID)
	}
	return g, pids, nil
}

func ignorable(err error) bool {
	return errors.Is(err, ErrProcessGone) || errors.Is(err, ErrIdentityChanged) || errors.Is(err, ErrNoConsole)
}

func (g *groupController) each(fn func(Controller) error) error {
	var primaryErr, otherErr error
	for _, m := range g.members {
		err := fn(m)
		switch {
		case m == g.primary:
			primaryErr = err
		case err != nil && otherErr == nil && !ignorable(err):
			otherErr = err
		}
	}
	if otherErr != nil && (primaryErr == nil || errors.Is(primaryErr, ErrProcessGone)) {
		return otherErr
	}
	return primaryErr
}

func (g *groupController) Graceful() error { return g.each(Controller.Graceful) }
func (g *groupController) Hard() error     { return g.each(Controller.Hard) }

func (g *groupController) Lifecycle() (model.Lifecycle, error) { return g.primary.Lifecycle() }
func (g *groupController) Meta() model.ProcMeta                { return g.primary.Meta() }

func (g *groupController) Close() error {
	var first error
	for _, m := range g.members {
		if err := m.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

var _ Controller = (*groupController)(nil)
