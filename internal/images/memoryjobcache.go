package images

import (
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

func NewInMemoryJobCache() JobCache {
	return &inMemoryJobCache{
		cache: make(map[types.NamespacedName]*Job),
		lock:  sync.RWMutex{},
	}
}

type inMemoryJobCache struct {
	cache map[types.NamespacedName]*Job
	lock  sync.RWMutex
}

func (c *inMemoryJobCache) Get(appName types.NamespacedName) *Job {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return c.cache[appName]
}

func (c *inMemoryJobCache) Set(appName types.NamespacedName, job *Job) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.cache[appName] = job
}

func (c *inMemoryJobCache) Delete(appName types.NamespacedName) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.cache, appName)
}

func (c *inMemoryJobCache) KeepOnly(appNames ...types.NamespacedName) []*Job {
	c.lock.Lock()
	defer c.lock.Unlock()
	keys := make(map[types.NamespacedName]bool, len(appNames))
	for _, n := range appNames {
		keys[n] = true
	}

	unwantedKeys := make([]types.NamespacedName, 0, len(appNames))
	for k := range c.cache {
		if keys[k] {
			continue
		}
		unwantedKeys = append(unwantedKeys, k)
	}

	prunedJobs := make([]*Job, 0, len(unwantedKeys))

	for _, k := range unwantedKeys {
		prunedJobs = append(prunedJobs, c.cache[k])
		delete(c.cache, k)
	}
	return prunedJobs
}

func (c *inMemoryJobCache) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.cache)
}

var _ JobCache = &inMemoryJobCache{}
