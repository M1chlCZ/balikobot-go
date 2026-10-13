//nolint:testpackage // White-box tests inject the cache clock.
package balikobot

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/m1chlcz/balikobot-go/carrier"
)

func testCapabilityRefresh(refreshes *atomic.Int32) func(context.Context) ([]Carrier, error) {
	return func(context.Context) ([]Carrier, error) {
		refreshes.Add(1)
		return []Carrier{{CarrierCode: carrier.PPL}}, nil
	}
}

func TestCapabilityCacheServesEntriesUntilTTL(t *testing.T) {
	t.Parallel()

	current := time.Unix(1_700_000_000, 0)
	cache := NewCapabilityCache(time.Minute)
	cache.now = func() time.Time { return current }
	var refreshes atomic.Int32
	refresh := testCapabilityRefresh(&refreshes)

	first, err := cache.Get(t.Context(), "account", refresh)
	if err != nil || len(first) != 1 || refreshes.Load() != 1 {
		t.Fatalf("first Get = %#v, %v, refreshes = %d", first, err, refreshes.Load())
	}
	second, err := cache.Get(t.Context(), "account", refresh)
	if err != nil || len(second) != 1 || refreshes.Load() != 1 {
		t.Fatalf("cached Get = %#v, %v, refreshes = %d", second, err, refreshes.Load())
	}
	current = current.Add(59 * time.Second)
	if _, err = cache.Get(t.Context(), "account", refresh); err != nil || refreshes.Load() != 1 {
		t.Fatalf("fresh Get = %v, refreshes = %d", err, refreshes.Load())
	}
	current = current.Add(2 * time.Second)
	if _, err = cache.Get(t.Context(), "account", refresh); err != nil || refreshes.Load() != 2 {
		t.Fatalf("expired Get = %v, refreshes = %d", err, refreshes.Load())
	}
}

func TestCapabilityCacheWithoutTTLRefreshesEveryGet(t *testing.T) {
	t.Parallel()

	cache := NewCapabilityCache(0)
	var refreshes atomic.Int32
	refresh := testCapabilityRefresh(&refreshes)
	for range 2 {
		if _, err := cache.Get(t.Context(), "account", refresh); err != nil {
			t.Fatalf("Get = %v", err)
		}
	}
	if refreshes.Load() != 2 {
		t.Fatalf("refreshes = %d, want 2 without a ttl", refreshes.Load())
	}
}

func TestCapabilityCacheSetAndInvalidate(t *testing.T) {
	t.Parallel()

	cache := NewCapabilityCache(time.Minute)
	var refreshes atomic.Int32
	refresh := testCapabilityRefresh(&refreshes)
	cache.Set("account", []Carrier{{CarrierCode: carrier.GLS}})
	carriers, err := cache.Get(t.Context(), "account", refresh)
	if err != nil || len(carriers) != 1 || carriers[0].CarrierCode != carrier.GLS ||
		refreshes.Load() != 0 {
		t.Fatalf("Get after Set = %#v, %v, refreshes = %d", carriers, err, refreshes.Load())
	}
	cache.Invalidate("account")
	carriers, err = cache.Get(t.Context(), "account", refresh)
	if err != nil || len(carriers) != 1 || refreshes.Load() != 1 {
		t.Fatalf("Get after Invalidate = %#v, %v, refreshes = %d", carriers, err, refreshes.Load())
	}
}

func TestCapabilityCacheDoesNotStoreFailedRefreshes(t *testing.T) {
	t.Parallel()

	cache := NewCapabilityCache(time.Minute)
	var refreshes atomic.Int32
	failing := func(context.Context) ([]Carrier, error) {
		refreshes.Add(1)
		return nil, ErrUnavailable
	}
	if _, err := cache.Get(t.Context(), "account", failing); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed Get = %v", err)
	}
	carriers, err := cache.Get(t.Context(), "account", testCapabilityRefresh(&refreshes))
	if err != nil || len(carriers) != 1 || refreshes.Load() != 2 {
		t.Fatalf("recovery Get = %#v, %v, refreshes = %d", carriers, err, refreshes.Load())
	}
}

func TestCapabilityCacheDeduplicatesConcurrentRefreshes(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cache := NewCapabilityCache(time.Minute)
		var refreshes atomic.Int32
		release := make(chan struct{})
		refresh := func(context.Context) ([]Carrier, error) {
			refreshes.Add(1)
			<-release
			return []Carrier{{CarrierCode: carrier.PPL}}, nil
		}
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				carriers, err := cache.Get(t.Context(), "account", refresh)
				if err != nil || len(carriers) != 1 {
					t.Errorf("Get = %#v, %v", carriers, err)
				}
			})
		}
		synctest.Wait()
		if refreshes.Load() != 1 {
			t.Fatalf("in-flight refreshes = %d, want 1", refreshes.Load())
		}
		close(release)
		workers.Wait()
		if refreshes.Load() != 1 {
			t.Fatalf("completed refreshes = %d, want 1", refreshes.Load())
		}
	})
}

func TestCapabilityCacheInvalidateDiscardsInFlightResult(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cache := NewCapabilityCache(time.Minute)
		var refreshes atomic.Int32
		release := make(chan struct{})
		refresh := func(context.Context) ([]Carrier, error) {
			refreshes.Add(1)
			<-release
			return []Carrier{{CarrierCode: carrier.PPL}}, nil
		}
		var leader sync.WaitGroup
		leader.Go(func() {
			if _, err := cache.Get(t.Context(), "account", refresh); err != nil {
				t.Errorf("leader Get = %v", err)
			}
		})
		synctest.Wait()
		cache.Invalidate("account")
		close(release)
		leader.Wait()
		carriers, err := cache.Get(t.Context(), "account", refresh)
		if err != nil || len(carriers) != 1 || refreshes.Load() != 2 {
			t.Fatalf("Get after invalidation = %#v, %v, refreshes = %d",
				carriers, err, refreshes.Load())
		}
	})
}

func TestWhoAmICacheStoresAccountInfo(t *testing.T) {
	t.Parallel()

	cache := NewWhoAmICache(time.Minute)
	var refreshes atomic.Int32
	refresh := func(context.Context) (WhoAmI, error) {
		refreshes.Add(1)
		return WhoAmI{Status: 200, Carriers: []WhoAmICarrier{{Slug: carrier.PPL}}}, nil
	}
	first, err := cache.Get(t.Context(), "account", refresh)
	if err != nil || first.Status != 200 || len(first.Carriers) != 1 {
		t.Fatalf("first Get = %#v, %v", first, err)
	}
	second, err := cache.Get(t.Context(), "account", refresh)
	if err != nil || second.Status != 200 || refreshes.Load() != 1 {
		t.Fatalf("cached Get = %#v, %v, refreshes = %d", second, err, refreshes.Load())
	}
}

func TestCapabilityCacheValidatesArguments(t *testing.T) {
	t.Parallel()

	cache := NewCapabilityCache(time.Minute)
	refresh := testCapabilityRefresh(new(atomic.Int32))
	for name, get := range map[string]func() error{
		"empty key": func() error {
			_, err := cache.Get(t.Context(), "", refresh)
			return err
		},
		"nil refresh": func() error {
			_, err := cache.Get(t.Context(), "account", nil)
			return err
		},
		"nil context": func() error {
			_, err := cache.Get(nil, "account", refresh)
			return err
		},
		"nil cache": func() error {
			var missing *DiscoveryCache[[]Carrier]
			_, err := missing.Get(t.Context(), "account", refresh)
			return err
		},
	} {
		if err := get(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s error = %v, want ErrInvalidRequest", name, err)
		}
	}

	missing := (*DiscoveryCache[[]Carrier])(nil)
	missing.Set("account", nil)
	missing.Invalidate("account")
	cache.Set("", []Carrier{{CarrierCode: carrier.PPL}})
	cache.Invalidate("")
}
