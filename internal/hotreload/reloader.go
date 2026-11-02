package hotreload

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/filewatch"
	"ezyapper/internal/logger"
)

// Pending is a prepared change. Commit makes it live and must not fail
// (anything that can fail belongs in Prepare); Abort releases what Prepare
// built. Exactly one of the two is called.
type Pending interface {
	Commit()
	Abort()
}

// Component is a part of the program that reacts to config changes.
type Component interface {
	// Name identifies the component in logs.
	Name() string
	// Prepare builds what moving from old to next needs. It returns nil
	// (and no error) when changes do not concern the component. It must
	// not change anything the running program can observe.
	Prepare(ctx context.Context, old, next *config.Config, changes Changes) (Pending, error)
}

// Funcs adapts a pair of functions to Pending. Either may be nil.
type Funcs struct {
	CommitFunc func()
	AbortFunc  func()
}

func (f Funcs) Commit() {
	if f.CommitFunc != nil {
		f.CommitFunc()
	}
}

func (f Funcs) Abort() {
	if f.AbortFunc != nil {
		f.AbortFunc()
	}
}

// Reloader owns the live config and applies new ones two-phase.
type Reloader struct {
	store *atomic.Value // *config.Config
	comps []Component

	// prepareTimeout bounds the whole prepare phase.
	prepareTimeout time.Duration
	// load reads and validates a config file (config.Load; replaceable in
	// tests).
	load func(path string) (*config.Config, error)

	// mu serializes Apply: the file watcher and the web dashboard can both
	// apply, and a reload must see the config the previous one committed.
	mu sync.Mutex

	watchMu sync.Mutex
	watcher *filewatch.Watcher
}

// DefaultPrepareTimeout bounds the prepare phase of one reload: it covers
// opening a Discord session, connecting MCP servers and building the memory
// service (which may migrate data).
const DefaultPrepareTimeout = 2 * time.Minute

// New returns a Reloader publishing configs to store. Components are
// prepared and committed in the order given.
func New(store *atomic.Value, comps ...Component) *Reloader {
	return &Reloader{
		store:          store,
		comps:          comps,
		prepareTimeout: DefaultPrepareTimeout,
		load:           config.Load,
	}
}

// Current returns the live config.
func (r *Reloader) Current() *config.Config {
	c, _ := r.store.Load().(*config.Config)
	return c
}

// ErrNoChange is returned by Apply when next equals the live config.
var ErrNoChange = errors.New("configuration unchanged")

// Apply validates next and makes it the live config, or keeps the current
// one if validation or any component's Prepare fails. It returns the
// changed key paths. Applying a config equal to the live one does nothing
// and returns ErrNoChange.
func (r *Reloader) Apply(ctx context.Context, next *config.Config) (Changes, error) {
	if next == nil {
		return nil, errors.New("hot reload: nil config")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.Current()
	if next.Path() == "" && old != nil {
		next.SetPath(old.Path())
	}
	if err := config.Validate(next); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	changes := Diff(old, next)
	if len(changes) == 0 {
		return nil, ErrNoChange
	}
	logger.Infof("[hotreload] %d config key(s) changed: %s", len(changes), changes)

	pctx, cancel := context.WithTimeout(ctx, r.prepareTimeout)
	defer cancel()

	type prepared struct {
		name string
		p    Pending
	}
	var done []prepared
	abortAll := func() {
		for i := len(done) - 1; i >= 0; i-- {
			safeCall(done[i].name+" abort", done[i].p.Abort)
		}
	}
	for _, c := range r.comps {
		p, err := safePrepare(pctx, c, old, next, changes)
		if err != nil {
			abortAll()
			return changes, fmt.Errorf("%s: %w", c.Name(), err)
		}
		if p != nil {
			done = append(done, prepared{name: c.Name(), p: p})
		}
	}

	// Publish first: committed components read the config through the
	// store, so they must see next once they switch over.
	r.store.Store(next)
	names := make([]string, 0, len(done))
	for _, d := range done {
		safeCall(d.name+" commit", d.p.Commit)
		names = append(names, d.name)
	}
	if len(names) > 0 {
		logger.Infof("[hotreload] configuration applied (reconfigured: %v)", names)
	} else {
		logger.Info("[hotreload] configuration applied (values are read live, nothing to rebuild)")
	}
	return changes, nil
}

// ReloadFile re-reads the file the live config came from and applies it.
// Errors are logged with a clear "keeping the previous configuration"
// message and returned.
func (r *Reloader) ReloadFile(ctx context.Context) error {
	cur := r.Current()
	if cur == nil || cur.Path() == "" {
		return errors.New("hot reload: the live config has no file path")
	}
	next, err := r.load(cur.Path())
	if err != nil {
		logger.Errorf("[hotreload] config reload rejected, keeping the previous configuration: %v", err)
		return err
	}
	_, err = r.Apply(ctx, next)
	switch {
	case errors.Is(err, ErrNoChange):
		logger.Debugf("[hotreload] %s changed on disk but no setting differs from the running config", cur.Path())
		return nil
	case err != nil:
		logger.Errorf("[hotreload] config reload failed, keeping the previous configuration: %v", err)
		return err
	}
	return nil
}

// Start begins watching the config file if operations.runtime.hot_reload
// is enabled in the live config. It is a no-op otherwise.
func (r *Reloader) Start() error {
	return r.syncWatcher(r.Current())
}

// Stop stops watching the config file.
func (r *Reloader) Stop() {
	r.watchMu.Lock()
	w := r.watcher
	r.watcher = nil
	r.watchMu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

// syncWatcher makes the watcher match cfg's hot_reload settings.
func (r *Reloader) syncWatcher(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	hr := cfg.Operations.HotReload
	if !hr.Enabled || cfg.Path() == "" {
		r.Stop()
		return nil
	}
	delay := time.Duration(hr.DebounceMs) * time.Millisecond

	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	if r.watcher != nil {
		r.watcher.SetDebounce(delay)
		return nil
	}
	w, err := filewatch.Watch(cfg.Path(), filewatch.Options{
		Debounce: delay,
		OnError: func(err error) {
			logger.Warnf("[hotreload] config watcher error: %v", err)
		},
	}, func() {
		_ = r.ReloadFile(context.Background())
	})
	if err != nil {
		return err
	}
	r.watcher = w
	logger.Infof("[hotreload] watching %s for changes (debounce %s)", w.Path(), delay)
	return nil
}

// WatcherComponent returns the Component that applies changes to
// operations.runtime.hot_reload itself (turning the watcher on or off,
// changing the debounce). Register it with New like any other component.
func (r *Reloader) WatcherComponent() Component { return watcherComponent{r} }

type watcherComponent struct{ r *Reloader }

func (watcherComponent) Name() string { return "config watcher" }

func (w watcherComponent) Prepare(_ context.Context, _, next *config.Config, changes Changes) (Pending, error) {
	if !changes.Any("operations.runtime.hot_reload") {
		return nil, nil
	}
	return Funcs{CommitFunc: func() {
		// Asynchronous: this commit usually runs inside the watcher's own
		// callback, and closing a watcher from its callback must not wait
		// for that callback.
		go func() {
			if err := w.r.syncWatcher(next); err != nil {
				logger.Errorf("[hotreload] could not start the config watcher: %v", err)
			} else if !next.Operations.HotReload.Enabled {
				logger.Info("[hotreload] config file watching disabled")
			}
		}()
	}}, nil
}

func safePrepare(ctx context.Context, c Component, old, next *config.Config, changes Changes) (p Pending, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			p, err = nil, fmt.Errorf("panic during prepare: %v", rec)
		}
	}()
	return c.Prepare(ctx, old, next, changes)
}

func safeCall(what string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Errorf("[hotreload] panic in %s: %v", what, rec)
		}
	}()
	fn()
}
