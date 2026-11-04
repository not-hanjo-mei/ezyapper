package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/logger"
)

// Reconfigurable is implemented by memory services whose embedder can be
// replaced at runtime without rebuilding the service. It is only used when
// the embedding endpoint or key changes but the model (and therefore the
// vector collection) stays the same. A new Qdrant connection, a new
// embedding model or dimension, or any setting baked into the service at
// construction rebuilds the whole service instead (Swappable.Replace):
// NewService selects the per-model vector collection, migrates legacy data
// and starts the re-embedding job, and none of that can be retrofitted onto a
// live service.
type Reconfigurable interface {
	// SwapEmbedder replaces the embedder.
	SwapEmbedder(e Embedder)
}

// Swappable is a Service proxy whose underlying service can be replaced at
// runtime. The bot and the web dashboard hold the proxy, so config hot
// reload can rebuild the memory subsystem (or switch it between enabled and
// the no-op service) without re-wiring them.
//
// A replaced service keeps serving calls that already reached it: it is
// closed after a grace period, not immediately.
type Swappable struct {
	cur atomic.Pointer[serviceBox]

	mu             sync.Mutex
	maintenanceCtx context.Context // non-nil once StartMaintenance succeeded
	closeGrace     time.Duration
}

type serviceBox struct{ s Service }

// defaultServiceCloseGrace bounds how long a replaced service may still be
// finishing calls before it is closed.
const defaultServiceCloseGrace = 30 * time.Second

// NewSwappable wraps s.
func NewSwappable(s Service) *Swappable {
	sw := &Swappable{closeGrace: defaultServiceCloseGrace}
	sw.cur.Store(&serviceBox{s: s})
	return sw
}

// Current returns the service calls are delegated to.
func (p *Swappable) Current() Service { return p.cur.Load().s }

// Replace installs next. If maintenance was started through the proxy it is
// stopped on the old service and started on next. The old service is
// closed after the grace period. It returns the old service.
func (p *Swappable) Replace(next Service) Service {
	if next == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.maintenanceCtx != nil {
		if err := next.StartMaintenance(p.maintenanceCtx); err != nil {
			logger.Warnf("[memory] maintenance start on replacement service failed: %v", err)
		}
	}
	old := p.cur.Swap(&serviceBox{s: next}).s
	if old != nil {
		old.StopMaintenance()
		grace := p.closeGrace
		time.AfterFunc(grace, func() {
			if err := old.Close(); err != nil {
				logger.Warnf("[memory] closing replaced memory service: %v", err)
			}
		})
	}
	return old
}

// SwapEmbedder forwards to the current service if it supports it.
func (p *Swappable) SwapEmbedder(e Embedder) {
	if r, ok := p.Current().(Reconfigurable); ok {
		r.SwapEmbedder(e)
		return
	}
	if e != nil {
		e.Stop()
	}
}

// --- Service delegation ---

func (p *Swappable) Store(ctx context.Context, m *Record) error { return p.Current().Store(ctx, m) }

func (p *Swappable) Search(ctx context.Context, userID, query string, opts *SearchOptions) ([]*Record, error) {
	return p.Current().Search(ctx, userID, query, opts)
}

func (p *Swappable) HybridSearch(ctx context.Context, userID, query string, keywords []string, opts *SearchOptions) ([]*Record, error) {
	return p.Current().HybridSearch(ctx, userID, query, keywords, opts)
}

func (p *Swappable) GetMemories(ctx context.Context, userID string, limit int) ([]*Record, error) {
	return p.Current().GetMemories(ctx, userID, limit)
}

func (p *Swappable) GetMemory(ctx context.Context, memoryID string) (*Record, error) {
	return p.Current().GetMemory(ctx, memoryID)
}

func (p *Swappable) DeleteMemory(ctx context.Context, memoryID string) error {
	return p.Current().DeleteMemory(ctx, memoryID)
}

func (p *Swappable) DeleteUserData(ctx context.Context, userID string) error {
	return p.Current().DeleteUserData(ctx, userID)
}

func (p *Swappable) SearchByMentionedUser(ctx context.Context, userID, mentionedUserID string, opts *SearchOptions) ([]*Record, error) {
	return p.Current().SearchByMentionedUser(ctx, userID, mentionedUserID, opts)
}

func (p *Swappable) SearchByChannel(ctx context.Context, channelID string, opts *SearchOptions) ([]*Record, error) {
	return p.Current().SearchByChannel(ctx, channelID, opts)
}

func (p *Swappable) GetStats(ctx context.Context) (*GlobalStats, error) {
	return p.Current().GetStats(ctx)
}

func (p *Swappable) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	return p.Current().GetProfile(ctx, userID)
}

func (p *Swappable) UpdateProfile(ctx context.Context, pr *Profile) error {
	return p.Current().UpdateProfile(ctx, pr)
}

func (p *Swappable) GetRelationships(ctx context.Context, userID string, relType RelationshipType) ([]*Relationship, error) {
	return p.Current().GetRelationships(ctx, userID, relType)
}

func (p *Swappable) GetRelationshipBetween(ctx context.Context, userA, userB string, relType RelationshipType) (*Relationship, error) {
	return p.Current().GetRelationshipBetween(ctx, userA, userB, relType)
}

func (p *Swappable) ConsolidateWithMessages(ctx context.Context, userID string, messages []*DiscordMessage) error {
	return p.Current().ConsolidateWithMessages(ctx, userID, messages)
}

func (p *Swappable) ConsolidateChannel(ctx context.Context, channelID string, messages []*DiscordMessage) error {
	return p.Current().ConsolidateChannel(ctx, channelID, messages)
}

func (p *Swappable) IncrementMessageCount(ctx context.Context, userID string) (int, error) {
	return p.Current().IncrementMessageCount(ctx, userID)
}

func (p *Swappable) IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error) {
	return p.Current().IncrementChannelMessageCount(ctx, channelID)
}

func (p *Swappable) ConsumeChannelMessageCount(channelID string, consumed int) int {
	return p.Current().ConsumeChannelMessageCount(channelID, consumed)
}

// StartMaintenance starts maintenance on the current service and remembers
// ctx so a replacement service gets maintenance too.
func (p *Swappable) StartMaintenance(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.Current().StartMaintenance(ctx); err != nil {
		return err
	}
	p.maintenanceCtx = ctx
	return nil
}

// StopMaintenance stops maintenance and forgets the remembered context.
func (p *Swappable) StopMaintenance() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maintenanceCtx = nil
	p.Current().StopMaintenance()
}

// Close closes the current service.
func (p *Swappable) Close() error { return p.Current().Close() }

var (
	_ Service        = (*Swappable)(nil)
	_ Reconfigurable = (*Swappable)(nil)
	_ Reconfigurable = (*MemoryService)(nil)
)
