package sim

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"
)

// ErrCitiesFull is returned when every visitor city is taken.
var ErrCitiesFull = errors.New("no free visitor city")

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// CitySessions gives each visitor a city of their own, so that one visitor's traffic settings, pause
// and reset do not reach the others. A city is created when its id is first used and removed when
// nobody has listened to it or commanded it for Idle.
type CitySessions struct {
	Max  int           // cities at the same time
	Idle time.Duration // unused time before a city is removed

	ctx    context.Context
	mu     sync.Mutex
	cities map[string]*citySession
}

type citySession struct {
	city      *CityService
	stop      context.CancelFunc
	listeners int
	used      time.Time
}

// NewCitySessions removes idle cities until ctx is cancelled; that also stops the cities.
func NewCitySessions(ctx context.Context, max int, idle time.Duration) *CitySessions {
	s := &CitySessions{Max: max, Idle: idle, ctx: ctx, cities: map[string]*citySession{}}
	go func() {
		tick := time.NewTicker(min(idle, 10*time.Second))
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				s.reap(now)
			}
		}
	}()
	return s
}

func (s *CitySessions) reap(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.cities {
		if c.listeners == 0 && now.Sub(c.used) >= s.Idle {
			c.stop()
			delete(s.cities, id)
		}
	}
}

func (s *CitySessions) session(id string) (*citySession, error) {
	if !sessionID.MatchString(id) {
		return nil, invalid("Ogiltigt besökar-id.")
	}
	c := s.cities[id]
	if c == nil {
		if len(s.cities) >= s.Max {
			return nil, ErrCitiesFull
		}
		ctx, stop := context.WithCancel(s.ctx)
		c = &citySession{city: NewCityService(), stop: stop}
		go c.city.Run(ctx)
		s.cities[id] = c
	}
	c.used = time.Now()
	return c, nil
}

// City returns the visitor's city for one query or command, creating it if needed.
func (s *CitySessions) City(id string) (*CityService, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.session(id)
	if err != nil {
		return nil, err
	}
	return c.city, nil
}

// Listen is City for a stream: the city is kept until release is called, however long that takes.
func (s *CitySessions) Listen(id string) (city *CityService, release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.session(id)
	if err != nil {
		return nil, nil, err
	}
	c.listeners++
	return c.city, sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		c.listeners--
		c.used = time.Now()
	}), nil
}

// Count is the number of visitor cities right now.
func (s *CitySessions) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cities)
}
