package images

import (
	"sync"
)

func NewCache[K comparable, V any]() *cache[K, V] {
	return &cache[K, V]{
		cache: make(map[K]V),
		lock:  sync.RWMutex{},
	}
}

type cache[K comparable, V any] struct {
	cache map[K]V
	lock  sync.RWMutex
}

func (c *cache[K, V]) Get(key K) *V {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if result, ok := c.cache[key]; ok {
		return &result
	}
	return nil
}

func (c *cache[K, V]) Set(key K, value V) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.cache[key] = value
}

func (c *cache[K, V]) Delete(key K) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.cache, key)
}

func (c *cache[K, V]) KeepOnly(keys ...K) map[K]V {
	c.lock.Lock()
	defer c.lock.Unlock()

	seen := make(map[K]bool, len(keys))
	for _, key := range keys {
		seen[key] = true
	}

	unwantedKeys := make([]K, 0, len(keys))
	for k := range c.cache {
		if seen[k] {
			continue
		}
		unwantedKeys = append(unwantedKeys, k)
	}

	pruned := make(map[K]V, len(unwantedKeys))
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
