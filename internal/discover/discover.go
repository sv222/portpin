// Package discover maps network endpoints to the processes that own them.
// The Resolver interface is implemented once per platform; the byte-level
// parsers next to it are pure and build on every platform so their tests run
// anywhere.
package discover

import "github.com/sv222/portpin/internal/model"

// Resolver finds socket bindings on the local machine.
type Resolver interface {
	// Resolve returns every binding matching f, including all members of an
	// SO_REUSEPORT cluster and every interface matched by a bare-port filter.
	Resolve(f model.Filter) ([]model.Binding, error)

	// ListAll returns every listening binding on the system.
	ListAll() ([]model.Binding, error)
}
