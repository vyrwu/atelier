package operator

import (
	"context"
	"errors"
	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"reflect"
	"testing"
	"time"
)

func TestWaitChanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	n := 0
	got, err := waitChanged(ctx, time.Millisecond, func() (map[string]core.AgentStatus, error) {
		n++
		if n < 3 {
			return map[string]core.AgentStatus{"a": core.StatusWorking, "b": core.StatusIdle}, nil
		}
		return map[string]core.AgentStatus{"a": core.StatusBlocked, "b": core.StatusIdle, "c": core.StatusIdle}, nil
	})
	if err != nil || !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("%v, %v", got, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	n = 0
	got, err = waitChanged(ctx2, time.Millisecond, func() (map[string]core.AgentStatus, error) {
		n++
		if n == 1 {
			return map[string]core.AgentStatus{"a": core.StatusWorking, "d": core.StatusBlocked}, nil
		}
		return map[string]core.AgentStatus{"a": core.StatusWorking}, nil
	})
	if err != nil || len(got) != 0 {
		t.Fatalf("%v, %v", got, err)
	}
	boom := errors.New("read failed")
	_, err = waitChanged(ctx, time.Millisecond, func() (map[string]core.AgentStatus, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestStatusesOf(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := agent.SetStatus("x", core.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	ws := []core.Workspace{{Slug: "x", Session: "x"}, {Slug: "y", Session: "y"}, {Slug: "r", Retired: true}, {Slug: core.MaitreSlug}}
	got := statusesOf(ws, map[string]bool{"x": true, "y": true})
	if !reflect.DeepEqual(got, map[string]core.AgentStatus{"x": core.StatusBlocked, "y": core.StatusIdle}) {
		t.Fatal(got)
	}
}
func TestDefaultWait(t *testing.T) {
	if DefaultWait("grok") != 50*time.Second || DefaultWait("codex") != 5*time.Minute || DefaultWait("") != 5*time.Minute {
		t.Fatal("bad timeout")
	}
}

func TestWaitChangedCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := waitChanged(ctx, time.Millisecond, func() (map[string]core.AgentStatus, error) {
		calls++
		cancel()
		return map[string]core.AgentStatus{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation returned %v after %d snapshots", err, calls)
	}
}
