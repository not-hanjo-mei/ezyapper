package memory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
)

type namedEmbedder struct {
	val     float32
	stopped atomic.Bool
}

func (e *namedEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{e.val}, nil
}
func (e *namedEmbedder) Stop() { e.stopped.Store(true) }

func TestSwappableEmbedder_SwapIsVisibleAndStopsOld(t *testing.T) {
	a := &namedEmbedder{val: 1}
	b := &namedEmbedder{val: 2}
	sw := NewSwappableEmbedder(a)

	if v, _ := sw.Embed(context.Background(), "x"); v[0] != 1 {
		t.Fatalf("before swap got %v", v)
	}
	if old := sw.Swap(b); old != a {
		t.Fatalf("Swap returned %v, want the old embedder", old)
	}
	if v, _ := sw.Embed(context.Background(), "x"); v[0] != 2 {
		t.Fatalf("after swap got %v, want the new embedder's output", v)
	}
	if !a.stopped.Load() {
		t.Error("old embedder was not stopped")
	}
	if b.stopped.Load() {
		t.Error("new embedder was stopped")
	}
	if sw.Swap(nil) != nil || sw.Current() != b {
		t.Error("Swap(nil) must be a no-op")
	}
	if NewSwappableEmbedder(sw) != sw {
		t.Error("wrapping a SwappableEmbedder must return it unchanged")
	}
}

func TestMemoryService_SwapEmbedderReachesConsolidator(t *testing.T) {
	a := &namedEmbedder{val: 1}
	svc, err := NewService(&ServiceConfig{
		ConsolidationInterval: 1, ShortTermLimit: 1,
		Consolidation: &config.ConsolidationConfig{Model: "m", SystemPrompt: "p"},
	}, testQdrantCfg, &QdrantClient{}, a, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	b := &namedEmbedder{val: 2}
	svc.SwapEmbedder(b)
	got, err := svc.consolidator.embedder.Embed(context.Background(), "x")
	if err != nil || got[0] != 2 {
		t.Fatalf("consolidator still uses the old embedder: %v %v", got, err)
	}
	got, _ = svc.embedder.Embed(context.Background(), "x")
	if got[0] != 2 {
		t.Fatalf("search still uses the old embedder: %v", got)
	}
}

// fakeService is a Service that records lifecycle calls.
type fakeService struct {
	NoopService
	name         string
	maintStarted atomic.Bool
	maintStopped atomic.Bool
	closed       atomic.Bool
	swapped      atomic.Bool
}

func (f *fakeService) GetStats(context.Context) (*GlobalStats, error) {
	return &GlobalStats{TotalMemories: int64(len(f.name))}, nil
}
func (f *fakeService) StartMaintenance(context.Context) error {
	if f.maintStarted.Load() {
		return errors.New("already started")
	}
	f.maintStarted.Store(true)
	return nil
}
func (f *fakeService) StopMaintenance()      { f.maintStopped.Store(true) }
func (f *fakeService) Close() error          { f.closed.Store(true); return nil }
func (f *fakeService) SwapEmbedder(Embedder) { f.swapped.Store(true) }

func TestSwappable_ReplaceDelegatesAndHandsOverMaintenance(t *testing.T) {
	a := &fakeService{name: "a"}
	b := &fakeService{name: "bb"}
	p := NewSwappable(a)
	p.closeGrace = 20 * time.Millisecond

	if err := p.StartMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := p.GetStats(context.Background()); st.TotalMemories != 1 {
		t.Fatalf("delegated to the wrong service: %+v", st)
	}

	if old := p.Replace(b); old != a {
		t.Fatal("Replace did not return the old service")
	}
	if st, _ := p.GetStats(context.Background()); st.TotalMemories != 2 {
		t.Fatalf("calls after Replace did not reach the new service: %+v", st)
	}
	if !b.maintStarted.Load() || !a.maintStopped.Load() {
		t.Error("maintenance was not handed over to the new service")
	}
	if a.closed.Load() {
		t.Error("old service closed before the grace period")
	}
	time.Sleep(80 * time.Millisecond)
	if !a.closed.Load() {
		t.Error("old service was not closed after the grace period")
	}
	if b.closed.Load() {
		t.Error("new service must stay open")
	}

	p.SwapEmbedder(&namedEmbedder{})
	if !b.swapped.Load() {
		t.Error("reconnect calls were not forwarded to the current service")
	}
}

func TestSwappable_NoopIgnoresReconnects(t *testing.T) {
	p := NewSwappable(NewNoopService())
	e := &namedEmbedder{}
	p.SwapEmbedder(e)
	if !e.stopped.Load() {
		t.Error("an embedder handed to the no-op service must be stopped, not leaked")
	}
}
