package chainkit

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// SelectionStrategyImpl is an interface for provider selection strategies
type SelectionStrategyImpl interface {
	// SelectProviders returns an ordered list of providers to try based on the strategy
	SelectProviders(available []ProviderConfig) []ProviderConfig

	// RecordAttempt records that a provider was attempted (used for round-robin, etc.)
	RecordAttempt(providerName string, priority int)

	// Reset resets the selection state (useful for testing or manual resets)
	Reset()
}

// priorityOnlySelector implements the priority-only strategy (current behavior)
type priorityOnlySelector struct{}

// NewPriorityOnlySelector creates a new priority-only selector
func NewPriorityOnlySelector() SelectionStrategyImpl {
	return &priorityOnlySelector{}
}

func (s *priorityOnlySelector) SelectProviders(available []ProviderConfig) []ProviderConfig {
	// Simply return providers in their current order (already sorted by priority)
	return available
}

func (s *priorityOnlySelector) RecordAttempt(providerName string, priority int) {
	// No state to track for priority-only
}

func (s *priorityOnlySelector) Reset() {
	// No state to reset
}

// roundRobinSelector implements round-robin selection among same-priority providers
type roundRobinSelector struct {
	mutex           sync.RWMutex
	priorityIndexes map[int]int // Maps priority level to current round-robin index
}

// NewRoundRobinSelector creates a new round-robin selector
func NewRoundRobinSelector() SelectionStrategyImpl {
	return &roundRobinSelector{
		priorityIndexes: make(map[int]int),
	}
}

func (s *roundRobinSelector) SelectProviders(available []ProviderConfig) []ProviderConfig {
	if len(available) == 0 {
		return available
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Group providers by priority
	priorityGroups := make(map[int][]ProviderConfig)
	priorities := make([]int, 0)

	for _, provider := range available {
		if _, exists := priorityGroups[provider.Priority]; !exists {
			priorities = append(priorities, provider.Priority)
			priorityGroups[provider.Priority] = make([]ProviderConfig, 0)
		}
		priorityGroups[provider.Priority] = append(priorityGroups[provider.Priority], provider)
	}

	// Build result with round-robin within each priority group
	result := make([]ProviderConfig, 0, len(available))

	for _, priority := range priorities {
		group := priorityGroups[priority]

		if len(group) == 1 {
			// Only one provider at this priority, just add it
			result = append(result, group[0])
		} else {
			// Multiple providers at this priority - apply round-robin
			currentIndex := s.priorityIndexes[priority]

			// Rotate the group based on current index
			rotated := make([]ProviderConfig, len(group))
			for i := 0; i < len(group); i++ {
				rotated[i] = group[(currentIndex+i)%len(group)]
			}
			result = append(result, rotated...)
		}
	}

	return result
}

func (s *roundRobinSelector) RecordAttempt(providerName string, priority int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Increment the round-robin index for this priority level
	s.priorityIndexes[priority] = (s.priorityIndexes[priority] + 1)
}

func (s *roundRobinSelector) Reset() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.priorityIndexes = make(map[int]int)
}

// randomSelector implements random selection among same-priority providers
type randomSelector struct {
	mutex sync.Mutex
	rng   *rand.Rand
}

// NewRandomSelector creates a new random selector
func NewRandomSelector() SelectionStrategyImpl {
	return &randomSelector{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *randomSelector) SelectProviders(available []ProviderConfig) []ProviderConfig {
	if len(available) == 0 {
		return available
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Group providers by priority
	priorityGroups := make(map[int][]ProviderConfig)
	priorities := make([]int, 0)

	for _, provider := range available {
		if _, exists := priorityGroups[provider.Priority]; !exists {
			priorities = append(priorities, provider.Priority)
			priorityGroups[provider.Priority] = make([]ProviderConfig, 0)
		}
		priorityGroups[provider.Priority] = append(priorityGroups[provider.Priority], provider)
	}

	// Build result with random ordering within each priority group
	result := make([]ProviderConfig, 0, len(available))

	for _, priority := range priorities {
		group := priorityGroups[priority]

		if len(group) == 1 {
			// Only one provider at this priority, just add it
			result = append(result, group[0])
		} else {
			// Multiple providers at this priority - shuffle them
			shuffled := make([]ProviderConfig, len(group))
			copy(shuffled, group)

			// Fisher-Yates shuffle
			for i := len(shuffled) - 1; i > 0; i-- {
				j := s.rng.Intn(i + 1)
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			}

			result = append(result, shuffled...)
		}
	}

	return result
}

func (s *randomSelector) RecordAttempt(providerName string, priority int) {
	// No state to track for random selection
}

func (s *randomSelector) Reset() {
	// Reset the random seed
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

// leastLoadedSelector implements least-loaded selection (based on success/failure stats)
type leastLoadedSelector struct {
	mutex          sync.RWMutex
	providerStats  map[string]*providerLoadStats
	failureTracker map[string]*FailureInfo // Reference to the manager's failure tracker
}

type providerLoadStats struct {
	activeRequests int64
	lastUsed       time.Time
}

// NewLeastLoadedSelector creates a new least-loaded selector
func NewLeastLoadedSelector(failureTracker map[string]*FailureInfo) SelectionStrategyImpl {
	return &leastLoadedSelector{
		providerStats:  make(map[string]*providerLoadStats),
		failureTracker: failureTracker,
	}
}

func (s *leastLoadedSelector) SelectProviders(available []ProviderConfig) []ProviderConfig {
	if len(available) == 0 {
		return available
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Group providers by priority
	priorityGroups := make(map[int][]ProviderConfig)
	priorities := make([]int, 0)

	for _, provider := range available {
		if _, exists := priorityGroups[provider.Priority]; !exists {
			priorities = append(priorities, provider.Priority)
			priorityGroups[provider.Priority] = make([]ProviderConfig, 0)
		}
		priorityGroups[provider.Priority] = append(priorityGroups[provider.Priority], provider)
	}

	// Build result with least-loaded ordering within each priority group
	result := make([]ProviderConfig, 0, len(available))

	for _, priority := range priorities {
		group := priorityGroups[priority]

		if len(group) == 1 {
			// Only one provider at this priority, just add it
			result = append(result, group[0])
		} else {
			// Multiple providers at this priority - sort by load
			sorted := make([]ProviderConfig, len(group))
			copy(sorted, group)

			// Sort by success rate and active requests
			for i := 0; i < len(sorted)-1; i++ {
				for j := i + 1; j < len(sorted); j++ {
					if s.getLoadScore(sorted[i].Name) > s.getLoadScore(sorted[j].Name) {
						sorted[i], sorted[j] = sorted[j], sorted[i]
					}
				}
			}

			result = append(result, sorted...)
		}
	}

	return result
}

func (s *leastLoadedSelector) getLoadScore(providerName string) float64 {
	// Lower score = better (less loaded)
	score := 0.0

	// Factor in active requests
	if stats, exists := s.providerStats[providerName]; exists {
		score += float64(stats.activeRequests) * 10.0
	}

	// Factor in failure rate
	if failure, exists := s.failureTracker[providerName]; exists {
		totalRequests := failure.TotalSuccesses + failure.TotalFailures
		if totalRequests > 0 {
			failureRate := float64(failure.TotalFailures) / float64(totalRequests)
			score += failureRate * 100.0
		}

		// Heavily penalize consecutive failures
		score += float64(failure.ConsecutiveFailures) * 50.0
	}

	return score
}

func (s *leastLoadedSelector) RecordAttempt(providerName string, priority int) {
	// This could be used to track active requests, but for now we don't need it
	// as the failure tracker already handles success/failure tracking
}

func (s *leastLoadedSelector) Reset() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.providerStats = make(map[string]*providerLoadStats)
}

// NewSelectionStrategyImpl creates a provider selector based on the selection strategy
func NewSelectionStrategyImpl(strategy SelectionStrategy, failureTracker map[string]*FailureInfo) (SelectionStrategyImpl, error) {
	switch strategy {
	case SelectionStrategyPriorityOnly:
		return NewPriorityOnlySelector(), nil
	case SelectionStrategyRoundRobin:
		return NewRoundRobinSelector(), nil
	case SelectionStrategyRandom:
		return NewRandomSelector(), nil
	case SelectionStrategyLeastLoaded:
		return NewLeastLoadedSelector(failureTracker), nil
	default:
		return nil, fmt.Errorf("unknown selection strategy: %s", strategy)
	}
}
