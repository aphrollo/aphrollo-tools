package config

import "time"

// Seconds is a key holding whole seconds, read as a budget: a negative number
// is no budget at all and keeps the key's default, so a mistyped value never
// turns into an instant timeout. Zero is a budget.
func (c *Config) Seconds(key string) time.Duration {
	n := c.Get(key).Value.N
	if n < 0 {
		n = defaultInt(key)
	}
	return time.Duration(n) * time.Second
}

// Positive is an integer key that only counts from one up: anything below keeps
// the key's default.
func (c *Config) Positive(key string) int {
	n := c.Get(key).Value.N
	if n < 1 {
		n = defaultInt(key)
	}
	return n
}

func defaultInt(key string) int {
	k, _ := Lookup(key)
	return k.Default.N
}
