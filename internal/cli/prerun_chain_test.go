package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestPreRunChain_ExecutesInOrder(t *testing.T) {
	var order []int

	hook1 := func(cmd *cobra.Command, args []string) { order = append(order, 1) }
	hook2 := func(cmd *cobra.Command, args []string) { order = append(order, 2) }
	hook3 := func(cmd *cobra.Command, args []string) { order = append(order, 3) }

	chain := PreRunChain(hook1, hook2, hook3)
	chain(&cobra.Command{}, []string{})

	if len(order) != 3 {
		t.Fatalf("expected 3 hooks to run, got %d", len(order))
	}
	for i, v := range order {
		if v != i+1 {
			t.Errorf("hook at position %d returned %d, expected %d", i, v, i+1)
		}
	}
}

func TestPreRunChain_SkipsNilHooks(t *testing.T) {
	var called bool

	hook := func(cmd *cobra.Command, args []string) { called = true }

	chain := PreRunChain(nil, hook, nil)
	chain(&cobra.Command{}, []string{})

	if !called {
		t.Error("expected non-nil hook to be called")
	}
}

func TestPreRunChain_EmptyChain(t *testing.T) {
	chain := PreRunChain()
	// Should not panic
	chain(&cobra.Command{}, []string{})
}

func TestPreRunChain_AllNil(t *testing.T) {
	chain := PreRunChain(nil, nil, nil)
	// Should not panic
	chain(&cobra.Command{}, []string{})
}
