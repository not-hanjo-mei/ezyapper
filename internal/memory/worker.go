package memory

import (
	"context"
	"sync"
	"time"

	"ezyapper/internal/logger"
)

// Trigger represents a consolidation trigger event
type Trigger struct {
	UserID       string
	ChannelID    string
	MessageCount int
	Timestamp    time.Time
}

// Worker processes consolidation triggers asynchronously
type Worker struct {
	consolidator *Consolidator
	triggers     chan Trigger
	mu           sync.Mutex
	pending      map[string]bool
}

// NewWorker creates a new consolidation worker
func NewWorker(consolidator *Consolidator) *Worker {
	return &Worker{
		consolidator: consolidator,
		triggers:     make(chan Trigger, 100),
		pending:      make(map[string]bool),
	}
}

// Start begins processing triggers in a background goroutine
func (w *Worker) Start(ctx context.Context) {
	go w.run(ctx)
}

func (w *Worker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case trigger := <-w.triggers:
			w.processTrigger(ctx, trigger)
		}
	}
}

func (w *Worker) processTrigger(ctx context.Context, trigger Trigger) {
	w.mu.Lock()
	if w.pending[trigger.UserID] {
		w.mu.Unlock()
		logger.Debugf("[consolidation] skipping duplicate trigger for user=%s", trigger.UserID)
		return
	}
	w.pending[trigger.UserID] = true
	w.mu.Unlock()

	logger.Infof("[consolidation] processing trigger for user=%s message_count=%d", trigger.UserID, trigger.MessageCount)

	defer func() {
		w.mu.Lock()
		delete(w.pending, trigger.UserID)
		w.mu.Unlock()
		logger.Debugf("[consolidation] cleared pending flag for user=%s", trigger.UserID)
	}()

	if err := w.consolidator.Process(ctx, trigger.UserID); err != nil {
		logger.Errorf("[consolidation] worker failed for user=%s: %v", trigger.UserID, err)
	}
}

// Trigger queues a consolidation trigger for processing
func (w *Worker) Trigger(userID string, channelID string, messageCount int) {
	w.triggers <- Trigger{
		UserID:       userID,
		ChannelID:    channelID,
		MessageCount: messageCount,
		Timestamp:    time.Now(),
	}
}

// IsPending returns whether a user has a pending consolidation
func (w *Worker) IsPending(userID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pending[userID]
}
