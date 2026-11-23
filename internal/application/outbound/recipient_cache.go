package outbound

import (
	"sync"
	"time"
)

const (
	defaultRegisteredTTL   = 24 * time.Hour
	defaultUnregisteredTTL = 5 * time.Minute
	recipientCacheCapacity = 10000
)

type cachedRegistration struct {
	registration Registration
	expiresAt    time.Time
}

type recipientCache struct {
	mu              sync.Mutex
	entries         map[string]cachedRegistration
	registeredTTL   time.Duration
	unregisteredTTL time.Duration
	now             func() time.Time
}

func newRecipientCache(registeredTTL, unregisteredTTL time.Duration, now func() time.Time) *recipientCache {
	return &recipientCache{
		entries:         map[string]cachedRegistration{},
		registeredTTL:   registeredTTL,
		unregisteredTTL: unregisteredTTL,
		now:             now,
	}
}

func (c *recipientCache) get(phone string) (Registration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.entries[phone]
	if !found {
		return Registration{}, false
	}
	if !entry.expiresAt.After(c.now()) {
		delete(c.entries, phone)
		return Registration{}, false
	}
	return entry.registration, true
}

func (c *recipientCache) put(phone string, registration Registration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= recipientCacheCapacity {
		c.evictExpired()
	}
	if len(c.entries) >= recipientCacheCapacity {
		c.entries = map[string]cachedRegistration{}
	}
	ttl := c.unregisteredTTL
	if registration.Exists {
		ttl = c.registeredTTL
	}
	c.entries[phone] = cachedRegistration{registration: registration, expiresAt: c.now().Add(ttl)}
}

func (c *recipientCache) evictExpired() {
	current := c.now()
	for phone, entry := range c.entries {
		if !entry.expiresAt.After(current) {
			delete(c.entries, phone)
		}
	}
}
