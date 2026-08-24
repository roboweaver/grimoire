package extensions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// ActionFunc is a side-effecting hook callback fired via DoAction. Payload is
// hook-specific (documented per hook name at its firing call site); callbacks
// must not assume a concrete type beyond what that call site's doc comment
// promises.
type ActionFunc func(ctx context.Context, payload any)

// FilterFunc transforms a value of type T, returning the (possibly unchanged)
// value or an error that short-circuits the remaining chain.
type FilterFunc[T any] func(ctx context.Context, value T) (T, error)

// filterFunc is the type-erased form a registered FilterFunc[T] is wrapped
// into so hooks of different value types can share one untyped map keyed by
// hook name. The registry itself never has to know T.
type filterFunc func(ctx context.Context, value any) (any, error)

// registry holds every action/filter registered for every hook name. There is
// exactly one process-wide instance (global, below): registration happens at
// init() time from any number of extension packages, and firing happens on
// every request from many goroutines, so all access is mutex-guarded.
type registry struct {
	mu      sync.RWMutex
	actions map[string][]ActionFunc
	filters map[string][]filterFunc
}

var global = &registry{
	actions: make(map[string][]ActionFunc),
	filters: make(map[string][]filterFunc),
}

// RegisterAction registers fn to run (in registration order, alongside any
// other registered actions) whenever hook fires via DoAction. Intended to be
// called from an extension's package-level init().
func RegisterAction(hook string, fn ActionFunc) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.actions[hook] = append(global.actions[hook], fn)
}

// RegisterFilter registers fn into the chain for hook. Intended to be called
// from an extension's package-level init(). T must match the type used at the
// corresponding ApplyFilters call site for the same hook name; the registry
// enforces this at fire-time via a type-asserting wrapper, since hooks of
// different value types share one untyped map keyed by hook name.
func RegisterFilter[T any](hook string, fn FilterFunc[T]) {
	wrapped := func(ctx context.Context, value any) (any, error) {
		typed, ok := value.(T)
		if !ok {
			return value, fmt.Errorf("extensions: filter registered for hook %q expected value type %T, got %T", hook, *new(T), value)
		}
		return fn(ctx, typed)
	}

	global.mu.Lock()
	defer global.mu.Unlock()
	global.filters[hook] = append(global.filters[hook], wrapped)
}

// DoAction invokes every action registered for hook, in registration order.
// A panicking callback is recovered and logged (with the hook name) so it
// cannot crash the caller; DoAction never returns an error. Firing an
// unregistered hook is a no-op.
func DoAction(ctx context.Context, hook string, payload any) {
	global.mu.RLock()
	fns := append([]ActionFunc(nil), global.actions[hook]...)
	global.mu.RUnlock()

	for _, fn := range fns {
		invokeAction(ctx, hook, fn, payload)
	}
}

func invokeAction(ctx context.Context, hook string, fn ActionFunc, payload any) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("extensions: action panicked", "hook", hook, "panic", r)
		}
	}()
	fn(ctx, payload)
}

// ApplyFilters runs value through every filter registered for hook, in
// registration order, each filter's output feeding the next filter's input.
// A filter returning an error stops the chain immediately; ApplyFilters
// returns that error alongside the value as it stood immediately before the
// erroring filter ran (the pre-error value), and no later filter in the
// chain observes anything past that point. A panicking filter callback is
// recovered the same way: the chain short-circuits, the pre-panic value is
// returned alongside a non-nil error, and the panic is logged with the hook
// name. Firing an unregistered hook returns the input value unchanged with a
// nil error.
func ApplyFilters[T any](ctx context.Context, hook string, value T) (T, error) {
	global.mu.RLock()
	fns := append([]filterFunc(nil), global.filters[hook]...)
	global.mu.RUnlock()

	current := value
	for _, fn := range fns {
		out, err := invokeFilter(ctx, hook, fn, current)
		if err != nil {
			return current, err
		}
		typed, ok := out.(T)
		if !ok {
			// Defensive: RegisterFilter's wrapper already guarantees this
			// cannot happen for a well-formed registration, but guard
			// against it anyway rather than silently corrupting the chain.
			return current, fmt.Errorf("extensions: filter for hook %q returned unexpected type %T", hook, out)
		}
		current = typed
	}
	return current, nil
}

func invokeFilter(ctx context.Context, hook string, fn filterFunc, value any) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("extensions: filter panicked", "hook", hook, "panic", r)
			out = value
			err = fmt.Errorf("extensions: filter for hook %q panicked: %v", hook, r)
		}
	}()
	return fn(ctx, value)
}
