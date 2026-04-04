package scoring

import (
	"context"
	"sync"
	"time"
)

// DecayManager handles the gradual recovery of provider scores over time
// by periodically reducing accumulated penalties
type DecayManager struct {
	config   ScoringConfig
	scores   map[string]*ProviderScore
	ticker   *time.Ticker
	stopChan chan struct{}
	running  bool
	mu       sync.RWMutex
}

// NewDecayManager creates a new decay manager
func NewDecayManager(config ScoringConfig, scores map[string]*ProviderScore) *DecayManager {
	return &DecayManager{
		config:   config,
		scores:   scores,
		stopChan: make(chan struct{}),
		running:  false,
	}
}

// Start begins the decay process in a background goroutine
func (dm *DecayManager) Start(ctx context.Context) {
	dm.mu.Lock()
	if dm.running {
		dm.mu.Unlock()
		return
	}

	dm.running = true
	dm.ticker = time.NewTicker(dm.config.DecayInterval)
	dm.mu.Unlock()

	go dm.run(ctx)
}

// Stop halts the decay process
func (dm *DecayManager) Stop() {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if !dm.running {
		return
	}

	dm.running = false
	close(dm.stopChan)

	if dm.ticker != nil {
		dm.ticker.Stop()
	}
}

// run is the main decay loop
func (dm *DecayManager) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			dm.Stop()
			return

		case <-dm.stopChan:
			return

		case <-dm.ticker.C:
			dm.applyDecay()
		}
	}
}

// applyDecay applies decay to all provider scores
func (dm *DecayManager) applyDecay() {
	dm.mu.RLock()
	scores := dm.scores
	decayRate := dm.config.DecayRate
	dm.mu.RUnlock()

	// Apply decay to each provider score
	for _, score := range scores {
		if score != nil {
			score.ApplyDecay(decayRate)
		}
	}
}

// UpdateConfig updates the decay configuration
// If the interval changes, the ticker is restarted
func (dm *DecayManager) UpdateConfig(config ScoringConfig) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	oldInterval := dm.config.DecayInterval
	dm.config = config

	// Restart ticker if interval changed and we're running
	if dm.running && oldInterval != config.DecayInterval {
		if dm.ticker != nil {
			dm.ticker.Stop()
		}
		dm.ticker = time.NewTicker(config.DecayInterval)
	}
}

// IsRunning returns whether the decay manager is currently running
func (dm *DecayManager) IsRunning() bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.running
}

// ForceDecay immediately applies decay to all scores (useful for testing)
func (dm *DecayManager) ForceDecay() {
	dm.applyDecay()
}

// UpdateScores updates the scores map (called when providers are added/removed)
func (dm *DecayManager) UpdateScores(scores map[string]*ProviderScore) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.scores = scores
}
