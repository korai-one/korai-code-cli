package todo_test

import (
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/todo"
)

func TestSetAndItems(t *testing.T) {
	t.Parallel()

	var l todo.List
	want := []todo.Item{
		{Content: "Write code", Status: todo.StatusCompleted},
		{Content: "Run tests", Status: todo.StatusInProgress, ActiveForm: "Running tests"},
		{Content: "Ship it", Status: todo.StatusPending},
	}
	l.Set(want)

	got := l.Items()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Items() mismatch (-want +got):\n%s", diff)
	}
}

func TestSetStoresCopy(t *testing.T) {
	t.Parallel()

	var l todo.List
	src := []todo.Item{{Content: "Original", Status: todo.StatusPending}}
	l.Set(src)

	// Mutating the source after Set must not change the stored list.
	src[0].Content = "Mutated"

	got := l.Items()
	if got[0].Content != "Original" {
		t.Errorf("Set retained caller slice: content = %q, want Original", got[0].Content)
	}
}

func TestItemsReturnsCopy(t *testing.T) {
	t.Parallel()

	var l todo.List
	l.Set([]todo.Item{{Content: "Task", Status: todo.StatusPending}})

	got := l.Items()
	got[0].Content = "Changed"

	again := l.Items()
	if again[0].Content != "Task" {
		t.Errorf("Items() returned a live reference: content = %q, want Task", again[0].Content)
	}
}

func TestRenderEmpty(t *testing.T) {
	t.Parallel()

	var l todo.List
	if got := l.Render(); got != "(no todos yet)" {
		t.Errorf("Render() empty = %q, want %q", got, "(no todos yet)")
	}
}

func TestRenderMix(t *testing.T) {
	t.Parallel()

	var l todo.List
	l.Set([]todo.Item{
		{Content: "Write code", Status: todo.StatusCompleted},
		{Content: "Run tests", Status: todo.StatusInProgress, ActiveForm: "Running tests"},
		{Content: "Ship it", Status: todo.StatusPending},
		{Content: "Cleanup", Status: todo.StatusInProgress}, // no ActiveForm: falls back to Content
	})

	want := "[x] Write code\n[~] Running tests\n[ ] Ship it\n[~] Cleanup"
	if got := l.Render(); got != want {
		t.Errorf("Render() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	var l todo.List
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Set([]todo.Item{{Content: "task", Status: todo.StatusPending}})
			_ = l.Items()
			_ = l.Render()
		}()
	}
	wg.Wait()
}
