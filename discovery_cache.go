package balikobot

import (
	"context"
	"sync"
	"time"
)

// DiscoveryCache is an in-memory TTL cache with single-flight refresh
// de-duplication for discovery results, for example the CarrierCapabilities
// and WhoAmI answers. Create it with NewCapabilityCache, NewWhoAmICache or
// NewDiscoveryCache.
//
// The cache is an explicit per-process accelerator. It cannot replace
// cross-process persistence: separate processes keep separate entries, and a
// durable shared store, for example a database-backed cache, must stay the
// authoritative source for the deployment. The cache performs no background
// eviction; the next Get replaces an expired entry.
//
// Every Get of the same key returns the same value, including its shared
// slices and maps, so callers must treat cached values as read-only.
type DiscoveryCache[T any] struct {
	mu       sync.Mutex
	ttl      time.Duration
	now      func() time.Time
	entries  map[string]cacheEntry[T]
	inflight map[string]*cacheRefresh[T]
}

type cacheEntry[T any] struct {
	value    T
	storedAt time.Time
}

type cacheRefresh[T any] struct {
	done    chan struct{}
	discard bool
	value   T
	err     error
}

// NewDiscoveryCache returns a cache whose entries expire after ttl. A ttl of
// zero or less disables reuse: every Get refreshes the value while concurrent
// Gets for the same key still share one refresh.
func NewDiscoveryCache[T any](ttl time.Duration) *DiscoveryCache[T] {
	return &DiscoveryCache[T]{
		ttl:      ttl,
		now:      time.Now,
		entries:  make(map[string]cacheEntry[T]),
		inflight: make(map[string]*cacheRefresh[T]),
	}
}

// NewCapabilityCache returns a DiscoveryCache for CarrierCapabilities results.
func NewCapabilityCache(ttl time.Duration) *DiscoveryCache[[]Carrier] {
	return NewDiscoveryCache[[]Carrier](ttl)
}

// NewWhoAmICache returns a DiscoveryCache for WhoAmI results.
func NewWhoAmICache(ttl time.Duration) *DiscoveryCache[WhoAmI] {
	return NewDiscoveryCache[WhoAmI](ttl)
}

// Get returns the cached value of key, or runs refresh and stores its result
// when the entry is missing or expired. Concurrent Gets for the same key share
// one refresh; the refresh runs with the context of the first caller, and
// every waiting caller receives the same value or error. A failed refresh is
// not stored, so the next Get resolves it again. An empty key, a nil context
// or a nil refresh returns ErrInvalidRequest.
func (cache *DiscoveryCache[T]) Get(
	ctx context.Context,
	key string,
	refresh func(context.Context) (T, error),
) (T, error) {
	var zero T
	if cache == nil || ctx == nil || key == "" || refresh == nil {
		return zero, ErrInvalidRequest
	}
	cache.mu.Lock()
	if entry, ok := cache.entries[key]; ok {
		age := cache.now().Sub(entry.storedAt)
		if age >= 0 && age < cache.ttl {
			cache.mu.Unlock()
			return entry.value, nil
		}
	}
	if waiting, ok := cache.inflight[key]; ok {
		cache.mu.Unlock()
		select {
		case <-waiting.done:
			if waiting.err != nil {
				return zero, waiting.err
			}
			return waiting.value, nil
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	call := &cacheRefresh[T]{done: make(chan struct{})}
	cache.inflight[key] = call
	cache.mu.Unlock()
	value, err := refresh(ctx)
	cache.mu.Lock()
	delete(cache.inflight, key)
	if err == nil && !call.discard {
		cache.entries[key] = cacheEntry[T]{value: value, storedAt: cache.now()}
	}
	call.value, call.err = value, err
	close(call.done)
	cache.mu.Unlock()
	return value, err
}

// Set stores value under key with the current time and ignores an empty key.
// A refresh already in flight is not cancelled; its result can overwrite a
// concurrent Set when it completes.
func (cache *DiscoveryCache[T]) Set(key string, value T) {
	if cache == nil || key == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries[key] = cacheEntry[T]{value: value, storedAt: cache.now()}
}

// Invalidate drops the cached value of key and ignores an empty key. A
// refresh already in flight is not cancelled: its result is returned to the
// callers that joined it but is not stored.
func (cache *DiscoveryCache[T]) Invalidate(key string) {
	if cache == nil || key == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.entries, key)
	if call, ok := cache.inflight[key]; ok {
		call.discard = true
	}
}
