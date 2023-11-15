package images

import (
	"sync"
)

type CacheKeyer interface {
	CacheKey() string
}

func NewCache[K CacheKeyer, V any]() *cache[K, V] {
	return &cache[K, V]{
		cache: make(map[string]V),
		lock:  sync.RWMutex{},
	}
}

type cache[K CacheKeyer, V any] struct {
	cache map[string]V
	lock  sync.RWMutex
}

func (c *cache[K, V]) Get(key K) *V {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if result, ok := c.cache[key.CacheKey()]; ok {
		return &result
	}
	return nil
}

func (c *cache[K, V]) Set(key K, value V) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.cache[key.CacheKey()] = value
}

func (c *cache[K, V]) Delete(key K) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.cache, key.CacheKey())
}

func (c *cache[K, V]) KeepOnly(keys ...K) map[string]V {
	c.lock.Lock()
	defer c.lock.Unlock()

	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		seen[key.CacheKey()] = true
	}

	unwantedKeys := make([]string, 0, len(keys))
	for k := range c.cache {
		if seen[k] {
			continue
		}
		unwantedKeys = append(unwantedKeys, k)
	}

	pruned := make(map[string]V, len(unwantedKeys))
	for _, k := range unwantedKeys {
		pruned[k] = c.cache[k]
		delete(c.cache, k)
	}

	return pruned
}

func (c *cache[K, V]) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.cache)
}
