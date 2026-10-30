package apiproxy

import (
	"regexp"
	"time"
)

var (
	coreRe     = regexp.MustCompile(`^/v1/(tracks|albums|artists)(/[^/]+)?/?$`)
	searchRe   = regexp.MustCompile(`^/v1/search`)
	volatileRe = regexp.MustCompile(`^/v1/(playlists|recommendations|browse|me|users/[^/]+/playlists|artists/[^/]+/(top-tracks|related-artists|albums))`)
)

// TTLConfig holds the cache TTL tiers.
type TTLConfig struct {
	Core, Search, Volatile, Default, Negative time.Duration
}

func DefaultTTLs() TTLConfig {
	return TTLConfig{
		Core:     7 * 24 * time.Hour,
		Search:   time.Hour,
		Volatile: 10 * time.Minute,
		Default:  time.Hour,
		Negative: 5 * time.Minute,
	}
}

// For picks a TTL for an upstream path.
func (c TTLConfig) For(path string) time.Duration {
	switch {
	case volatileRe.MatchString(path):
		return c.Volatile
	case searchRe.MatchString(path):
		return c.Search
	case coreRe.MatchString(path):
		return c.Core
	default:
		return c.Default
	}
}
