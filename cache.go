package iris

import (
	"sync"
)

// IRouterCache is the interface which the MemoryRouter implements
type IRouterCache interface {
	OnTick()
	AddItem(method, url string, ctx *Context)
	GetItem(method, url string) *Context
	SetMaxItems(maxItems int)
}

// MemoryRouterCache creation done with just &MemoryRouterCache{}
type MemoryRouterCache struct {
	//1. map[string] ,key is HTTP Method(GET,POST...)
	//2. map[string]*Context ,key is The Request URL Path
	//the map in this case is the faster way, I tried with array of structs but it's 100 times slower on > 1 core because of async goroutes on addItem I sugges, so we keep the map
	items    map[string]map[string]*Context
	MaxItems int
	//we need this mutex if we have running the iris at > 1 core, because we use map but maybe at the future I will change it.
	mu *sync.Mutex
}

// SetMaxItems receives int and set max cached items to this number
func (mc *MemoryRouterCache) SetMaxItems(_itemslen int) {
	mc.MaxItems = _itemslen
}

// NewMemoryRouterCache returns the cache for a router, is used on the MemoryRouter
func NewMemoryRouterCache() *MemoryRouterCache {
	mc := &MemoryRouterCache{mu: &sync.Mutex{}, items: make(map[string]map[string]*Context, 0)}
	mc.resetBag()
	return mc
}

// AddItem adds an item to the bag/cache, is a goroutine.
func (mc *MemoryRouterCache) AddItem(method, url string, ctx *Context) {
	go func(method, url string, context *Context) { //for safety on multiple fast calls
		mc.mu.Lock()
		mc.items[method][url] = context
		mc.mu.Unlock()
	}(method, url, ctx)
}

// GetItem returns an item from the bag/cache, if not exists it returns just nil.
func (mc *MemoryRouterCache) GetItem(method, url string) *Context {
	//Don't check for anything else, make it as fast as it can be.
	mc.mu.Lock()
	if ctx := mc.items[method][url]; ctx != nil {
		mc.mu.Unlock()
		return ctx
	}
	mc.mu.Unlock()
	return nil
}

// OnTick is the implementation of the ITick
// it makes the MemoryRouterCache a ticker's listener
func (mc *MemoryRouterCache) OnTick() {

	mc.mu.Lock()
	if mc.MaxItems == 0 {
		//just reset to complete new maps all methods
		mc.resetBag()
	} else {
		//loop each method on bag and clear it if it's len is more than MaxItems
		for k, v := range mc.items {
			if len(v) >= mc.MaxItems {
				//we just create a new map, no delete each manualy because this number maybe be very long.
				mc.items[k] = make(map[string]*Context, 0)
			}
		}
	}

	mc.mu.Unlock()
}

// resetBag clears the cached items
func (mc *MemoryRouterCache) resetBag() {
	for _, m := range HTTPMethods.ANY {
		mc.items[m] = make(map[string]*Context, 0)
	}
}
