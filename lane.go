package seriallane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrCleanupFailed  = errors.New("cleanup failed")
	ErrPanicRecovered = errors.New("panic recovered in job")
)

type Key string

func (k Key) Sub(id string) Key {
	return Key(string(k) + ":" + id)
}

func Namespace(ns string, id string) Key {
	return Key(ns + ":" + id)
}

type JobFunc func(ctx context.Context) error

type lane struct {
	mu      sync.Mutex
	lastRun time.Time
}

type Manager struct {
	mu          sync.RWMutex
	lanes       map[Key]*lane
	idleTimeout time.Duration
}

func New(idleTimeout time.Duration) *Manager {
	return &Manager{lanes: make(map[Key]*lane)}
}

func (m *Manager) getLane(key Key) *lane {
	m.mu.RLock()
	l, ok := m.lanes[key]
	m.mu.RUnlock()
	if ok {
		return l
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check again: did someone else create it while we switched locks?
	if l, ok = m.lanes[key]; !ok {
		l = &lane{}
		m.lanes[key] = l
	}
	return l
}

func (m *Manager) Do(ctx context.Context, key Key, fn JobFunc) error {
	lane := m.getLane(key)

	lane.mu.Lock()
	defer lane.mu.Unlock()

	lane.lastRun = time.Now()
	return m.run(fn, ctx)
}

func (m *Manager) DoMulti(ctx context.Context, keys []Key, fn JobFunc) error {
	if len(keys) == 0 {
		return fn(ctx)
	}

	// Clone the slice to respect the caller's data
	orderedKeys := make([]Key, len(keys))
	copy(orderedKeys, keys)

	// Sort the clone to prevent lock inversion/deadlocks
	sort.Slice(orderedKeys, func(i, j int) bool {
		return orderedKeys[i] < orderedKeys[j]
	})

	lanes := make([]*lane, len(orderedKeys))
	for i, k := range orderedKeys {
		lanes[i] = m.getLane(k)
	}

	for _, l := range lanes {
		l.mu.Lock()
		l.lastRun = time.Now()
	}

	defer func() {
		for i := len(lanes) - 1; i >= 0; i-- {
			lanes[i].mu.Unlock()
		}
	}()

	return m.run(fn, ctx)
}

func (m *Manager) run(fn JobFunc, ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrPanicRecovered, r)
		}
	}()

	return fn(ctx)
}

func (m *Manager) CleanupStaleLanes() error {
	now := time.Now()
	var stale []Key

	m.mu.RLock()
	for key, l := range m.lanes {
		l.mu.Lock()
		last := l.lastRun
		l.mu.Unlock()

		if now.Sub(last) > m.idleTimeout {
			stale = append(stale, key)
		}
	}
	m.mu.RUnlock()

	if len(stale) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range stale {
		if l, ok := m.lanes[key]; ok {
			l.mu.Lock()
			isStillStale := now.Sub(l.lastRun) > m.idleTimeout
			l.mu.Unlock()

			if isStillStale {
				delete(m.lanes, key)
			}
		}
	}

	return nil
}

// CleanupService provides a managed background worker.
func (m *Manager) CleanupService(interval time.Duration) *cleanupService {
	return &cleanupService{
		manager:  m,
		interval: interval,
	}
}

type cleanupService struct {
	manager  *Manager
	interval time.Duration
}

func (s *cleanupService) Run(ctx context.Context) error {
	if s.interval <= 0 {
		<-ctx.Done()
		return ctx.Err()
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.manager.CleanupStaleLanes(); err != nil {
				return fmt.Errorf("%w: %v", ErrCleanupFailed, err)
			}
		}
	}
}
