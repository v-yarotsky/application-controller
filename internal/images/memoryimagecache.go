package images

import (
	"encoding/json"
	"sync"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
)

func NewInMemoryImageCache() *inMemoryImageCache {
	return &inMemoryImageCache{
		cache: make(map[string]ImageRef),
		lock:  sync.RWMutex{},
	}
}

type inMemoryImageCache struct {
	cache map[string]ImageRef
	lock  sync.RWMutex
}

func (c *inMemoryImageCache) Get(spec yarotskymev1alpha1.ImageSpec) *ImageRef {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if result, ok := c.cache[c.key(spec)]; ok {
		return &result
	}
	return nil
}

func (c *inMemoryImageCache) Set(spec yarotskymev1alpha1.ImageSpec, ref ImageRef) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.cache[c.key(spec)] = ref
}

func (c *inMemoryImageCache) Delete(spec yarotskymev1alpha1.ImageSpec) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.cache, c.key(spec))
}

func (c *inMemoryImageCache) KeepOnly(specs ...yarotskymev1alpha1.ImageSpec) []ImageRef {
	c.lock.Lock()
	defer c.lock.Unlock()

	keys := make(map[string]bool, len(specs))
	for _, spec := range specs {
		keys[c.key(spec)] = true
	}

	unwantedKeys := make([]string, 0, len(specs))
	for k := range c.cache {
		if keys[k] {
			continue
		}
		unwantedKeys = append(unwantedKeys, k)
	}

	prunedImageRefs := make([]ImageRef, 0, len(unwantedKeys))
	for _, k := range unwantedKeys {
		prunedImageRefs = append(prunedImageRefs, c.cache[k])
		delete(c.cache, k)
	}

	return prunedImageRefs
}

func (c *inMemoryImageCache) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.cache)
}

func (c *inMemoryImageCache) key(spec yarotskymev1alpha1.ImageSpec) string {
	keyBytes, _ := json.Marshal(spec)
	return string(keyBytes)
}

var _ ImageCache = &inMemoryImageCache{}
