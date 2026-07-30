package integrations

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
)

type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]Provider)}
}

// Register adds one adapter under its stable provider kind. Registration is
// intentionally strict so a later adapter cannot silently replace another.
func (r *ProviderRegistry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("%w: provider is nil", ErrInvalidConfiguration)
	}
	kind := strings.TrimSpace(provider.Metadata().Kind)
	if kind == "" {
		return fmt.Errorf("%w: provider kind is required", ErrInvalidConfiguration)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[kind]; exists {
		log.Printf("integration provider registration rejected for %q: duplicate kind", kind)
		return fmt.Errorf("%w: %s", ErrProviderAlreadyExists, kind)
	}
	r.providers[kind] = provider
	return nil
}

func (r *ProviderRegistry) Get(kind string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, exists := r.providers[strings.TrimSpace(kind)]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, kind)
	}
	return provider, nil
}

func (r *ProviderRegistry) List() []ProviderMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]ProviderMetadata, 0, len(r.providers))
	for _, provider := range r.providers {
		items = append(items, provider.Metadata())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind < items[j].Kind })
	return items
}
