package dnscache

import (
	"fmt"
	"net"
	"sync"
	"time"
)

type item struct {
	value     string
	expiresAt time.Time
}

// Cache is simple key-value cache.
type Cache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	items map[string]item
}

func New(ttl time.Duration) *Cache {
	return &Cache{
		ttl:   ttl,
		items: make(map[string]item),
	}
}

func (c *Cache) get(key string) (string, bool) {
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		return "", false
	}

	if time.Now().After(it.expiresAt) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return "", false
	}

	return it.value, true
}

func (c *Cache) set(key, value string) {
	c.mu.Lock()
	c.items[key] = item{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// LookupIPAddress returns IPAddress by hostname provided
func (c *Cache) LookupIPAddress(hostname string) (string, error) {
	var (
		IPs []net.IP
		err error
	)

	if ipaddress, exist := c.get(hostname); exist {
		return ipaddress, nil
	}

	IPs, err = net.LookupIP(hostname)
	if err != nil {
		return "", fmt.Errorf("unable to lookup ip by hostname %s, err: %w", hostname, err)
	}

	for _, ip := range IPs {
		ipV4 := ip.To4()
		if ipV4 == nil {
			continue
		}

		// we have always only one IPAddress for the node.
		c.set(hostname, ipV4.String())
		return ipV4.String(), nil
	}

	return "", fmt.Errorf("unable to lookup ip by hostname %s: no IPv4 addresses", hostname)
}
