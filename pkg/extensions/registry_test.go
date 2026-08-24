package extensions

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestDoAction_RunsInRegistrationOrder(t *testing.T) {
	hook := "test.action.order"
	var mu sync.Mutex
	var got []int

	for i := 0; i < 5; i++ {
		i := i
		RegisterAction(hook, func(ctx context.Context, payload any) {
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
		})
	}

	DoAction(context.Background(), hook, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 5 {
		t.Fatalf("expected 5 actions to run, got %d", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("expected registration order %v, got %v", []int{0, 1, 2, 3, 4}, got)
		}
	}
}

func TestDoAction_PanicRecoveredDoesNotStopLaterActions(t *testing.T) {
	hook := "test.action.panic"
	var mu sync.Mutex
	var ran []string

	RegisterAction(hook, func(ctx context.Context, payload any) {
		mu.Lock()
		ran = append(ran, "first")
		mu.Unlock()
		panic("boom")
	})
	RegisterAction(hook, func(ctx context.Context, payload any) {
		mu.Lock()
		ran = append(ran, "second")
		mu.Unlock()
	})

	// Must not panic out of DoAction itself.
	DoAction(context.Background(), hook, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[0] != "first" || ran[1] != "second" {
		t.Fatalf("expected both actions to run despite panic, got %v", ran)
	}
}

func TestDoAction_UnregisteredHookIsNoOp(t *testing.T) {
	// Must not panic and must simply do nothing.
	DoAction(context.Background(), "test.action.unregistered.never-registered", "payload")
}

func TestApplyFilters_ChainsInRegistrationOrder(t *testing.T) {
	hook := "test.filter.chain"
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		return v + 1, nil
	})
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		return v * 2, nil
	})

	got, err := ApplyFilters(context.Background(), hook, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// (3 + 1) * 2 = 8
	if got != 8 {
		t.Fatalf("expected 8, got %d", got)
	}
}

func TestApplyFilters_ErrorShortCircuitsChain(t *testing.T) {
	hook := "test.filter.error"
	sentinel := errors.New("filter failed")
	var thirdCalled bool

	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		return v + 1, nil
	})
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		return v, sentinel
	})
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		thirdCalled = true
		return v + 100, nil
	})

	got, err := ApplyFilters(context.Background(), hook, 3)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	// pre-error value: the value fed INTO the erroring filter (3+1=4), not
	// mutated further downstream.
	if got != 4 {
		t.Fatalf("expected pre-error value 4, got %d", got)
	}
	if thirdCalled {
		t.Fatal("expected downstream filter to never run after an error short-circuits the chain")
	}
}

func TestApplyFilters_PanicRecoveredShortCircuitsAndReturnsPrePanicValue(t *testing.T) {
	hook := "test.filter.panic"
	var thirdCalled bool

	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		return v + 1, nil
	})
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		panic("filter exploded")
	})
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		thirdCalled = true
		return v + 100, nil
	})

	got, err := ApplyFilters(context.Background(), hook, 3)
	if err == nil {
		t.Fatal("expected a non-nil error from a recovered panic")
	}
	if got != 4 {
		t.Fatalf("expected pre-panic value 4, got %d", got)
	}
	if thirdCalled {
		t.Fatal("expected downstream filter to never run after a panic short-circuits the chain")
	}
}

func TestApplyFilters_UnregisteredHookReturnsInputUnchanged(t *testing.T) {
	got, err := ApplyFilters(context.Background(), "test.filter.unregistered.never-registered", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected input unchanged, got %q", got)
	}
}

func TestRegistry_ConcurrentAccessIsRaceFree(t *testing.T) {
	hook := "test.concurrent"
	RegisterAction(hook, func(ctx context.Context, payload any) {})
	RegisterFilter(hook, func(ctx context.Context, v int) (int, error) {
		return v + 1, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			DoAction(context.Background(), hook, i)
		}()
		go func() {
			defer wg.Done()
			if _, err := ApplyFilters(context.Background(), hook, i); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
}
