package chainkit

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// selectionStrategy is an interface for provider selection strategies
type selectionStrategy interface {
	// SelectProviders returns an ordered list of providers to try based on the strategy
	SelectProviders(available []providerConfig) []providerConfig

	// RecordAttempt records that a provider was attempted (used for round-robin, etc.)
	RecordAttempt(providerName string, priority int)

	// Reset resets the selection state (useful for testing or manual resets)
	Reset()
}

// priorityOnlySelector implements the priority-only strategy (current behavior)
type priorityOnlySelector struct{}

func newPriorityOnlySelector() selectionStrategy {
	return &priorityOnlySelector{}
}

func (s *priorityOnlySelector) SelectProviders(available []providerConfig) []providerConfig {
	return available
}

func (s *priorityOnlySelector) RecordAttempt(providerName string, priority int) {}

func (s *priorityOnlySelector) Reset() {}

// roundRobinSelector implements round-robin selection among same-priority providers
type roundRobinSelector struct {
	mutex           sync.RWMutex
	priorityIndexes map[int]int
}

func newRoundRobinSelector() selectionStrategy {
	return &roundRobinSelector{
		priorityIndexes: make(map[int]int),
	}
}

func (s *roundRobinSelector) SelectProviders(available []providerConfig) []providerConfig {
	if len(available) == 0 {
		return available
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	priorityGroups := make(map[int][]providerConfig)
	priorities := make([]int, 0)

	for _, provider := range available {
		if _, exists := priorityGroups[provider.Priority]; !exists {
			priorities = append(priorities, provider.Priority)
			priorityGroups[provider.Priority] = make([]providerConfig, 0)
		}
		priorityGroups[provider.Priority] = append(priorityGroups[provider.Priority], provider)
	}
	sort.Ints(priorities)

	result := make([]providerConfig, 0, len(available))

	for _, priority := range priorities {
		group := priorityGroups[priority]
		if len(group) == 1 {
			result = append(result, group[0])
		} else {
			currentIndex := s.priorityIndexes[priority] % len(group)
			rotated := make([]providerConfig, len(group))
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

func newRandomSelector() selectionStrategy {
	return &randomSelector{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *randomSelector) SelectProviders(available []providerConfig) []providerConfig {
	if len(available) == 0 {
		return available
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	priorityGroups := make(map[int][]providerConfig)
	priorities := make([]int, 0)

	for _, provider := range available {
		if _, exists := priorityGroups[provider.Priority]; !exists {
			priorities = append(priorities, provider.Priority)
			priorityGroups[provider.Priority] = make([]providerConfig, 0)
		}
		priorityGroups[provider.Priority] = append(priorityGroups[provider.Priority], provider)
	}
	sort.Ints(priorities)

	result := make([]providerConfig, 0, len(available))

	for _, priority := range priorities {
		group := priorityGroups[priority]
		if len(group) == 1 {
			result = append(result, group[0])
		} else {
			shuffled := make([]providerConfig, len(group))
			copy(shuffled, group)
			for i := len(shuffled) - 1; i > 0; i-- {
				j := s.rng.Intn(i + 1)
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			}
			result = append(result, shuffled...)
		}
	}

	return result
}

func (s *randomSelector) RecordAttempt(providerName string, priority int) {}

func (s *randomSelector) Reset() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

// leastLoadedSelector implements least-loaded selection (based on success/failure stats)
type leastLoadedSelector struct {
	mutex          sync.RWMutex
	providerStats  map[string]*providerLoadStats
	failureTracker map[string]*failureInfo
}

type providerLoadStats struct {
	activeRequests int64
}

func newLeastLoadedSelector(ft map[string]*failureInfo) selectionStrategy {
	return &leastLoadedSelector{
		providerStats:  make(map[string]*providerLoadStats),
		failureTracker: ft,
	}
}

func (s *leastLoadedSelector) SelectProviders(available []providerConfig) []providerConfig {
	if len(available) == 0 {
		return available
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	priorityGroups := make(map[int][]providerConfig)
	priorities := make([]int, 0)

	for _, provider := range available {
		if _, exists := priorityGroups[provider.Priority]; !exists {
			priorities = append(priorities, provider.Priority)
			priorityGroups[provider.Priority] = make([]providerConfig, 0)
		}
		priorityGroups[provider.Priority] = append(priorityGroups[provider.Priority], provider)
	}
	sort.Ints(priorities)

	result := make([]providerConfig, 0, len(available))

	for _, priority := range priorities {
		group := priorityGroups[priority]
		if len(group) == 1 {
			result = append(result, group[0])
		} else {
			sorted := make([]providerConfig, len(group))
			copy(sorted, group)
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
	score := 0.0
	if stats, exists := s.providerStats[providerName]; exists {
		score += float64(stats.activeRequests) * 10.0
	}
	if failure, exists := s.failureTracker[providerName]; exists {
		totalRequests := failure.TotalSuccesses + failure.TotalFailures
		if totalRequests > 0 {
			failureRate := float64(failure.TotalFailures) / float64(totalRequests)
			score += failureRate * 100.0
		}
		score += float64(failure.ConsecutiveFailures) * 50.0
	}
	return score
}

func (s *leastLoadedSelector) RecordAttempt(providerName string, priority int) {}

func (s *leastLoadedSelector) Reset() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.providerStats = make(map[string]*providerLoadStats)
}

func newSelectionStrategy(strategy SelectionStrategy, ft map[string]*failureInfo) (selectionStrategy, error) {
	switch strategy {
	case SelectionStrategyPriorityOnly:
		return newPriorityOnlySelector(), nil
	case SelectionStrategyRoundRobin:
		return newRoundRobinSelector(), nil
	case SelectionStrategyRandom:
		return newRandomSelector(), nil
	case SelectionStrategyLeastLoaded:
		return newLeastLoadedSelector(ft), nil
	default:
		return nil, fmt.Errorf("unknown selection strategy: %s", strategy)
	}
}
