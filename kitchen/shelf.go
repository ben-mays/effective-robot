package kitchen

import (
	"fmt"
	"sync"
)

// Shelf is a container interface for Orders. Shelf implementations must be thread-safe.
type Shelf interface {

	// Name returns a unique name for the shelf. Optional.
	Name() string

	// Supported returns the list of order types that are supported by this shelf.
	Supported() []string

	// Orders returns an unsorted array of Orders
	Orders() []*Order

	// Put places an order on the shelf
	Get(string) (*Order, error)

	// Put places an order on the shelf
	Put(*Order) error

	// Remove removes an order from the shelf
	Remove(string) error

	// Capacity returns the number of orders that the shelf can hold.
	Capacity() int

	// Decay returns the rate of decay.
	Decay() float64
}

// StaticShelf is an implementation of the Shelf interface that has a fixed decay rate, capacity and order types.
type staticShelf struct {
	sync.RWMutex

	name      string
	orders    map[string]*Order
	numOrders int
	capacity  int
	supported []string
	decayRate float64
}

func (s *staticShelf) Name() string {
	return s.name
}

func (s *staticShelf) Orders() []*Order {
	s.RLock()
	defer s.RUnlock()
	orders := make([]*Order, s.numOrders)
	count := 0
	for _, v := range s.orders {
		orders[count] = v
		count++
	}
	return orders
}

func (s *staticShelf) Get(orderID string) (*Order, error) {
	s.Lock()
	defer s.Unlock()
	// check if its already there, noop
	order, exists := s.orders[orderID]
	if !exists {
		return nil, fmt.Errorf("order %s not present in shelf %s", orderID, s.name)
	}
	return order, nil
}

func (s *staticShelf) Put(o *Order) error {
	s.Lock()
	defer s.Unlock()
	// check if its already there, noop
	if _, exists := s.orders[o.ID()]; exists {
		return nil
	}
	if s.numOrders >= s.capacity {
		return fmt.Errorf("failed to put order on shelf, staticShelf is at capacity %d", s.capacity)
	}
	s.numOrders++
	s.orders[o.ID()] = o
	return nil
}

func (s *staticShelf) Remove(orderID string) error {
	s.Lock()
	defer s.Unlock()
	if _, exists := s.orders[orderID]; !exists {
		return fmt.Errorf("attempted to remove order %s that does not exist", orderID)
	}
	s.numOrders--
	delete(s.orders, orderID)

	return nil
}

func (s *staticShelf) Supported() []string {
	return s.supported
}

func (s *staticShelf) Capacity() int {
	return s.capacity
}

func (s *staticShelf) Decay() float64 {
	return s.decayRate
}

func NewStaticShelf(name string, capacity int, supported []string, decayRate float64) Shelf {
	orders := make(map[string]*Order, capacity)
	return &staticShelf{
		name:      name,
		orders:    orders,
		capacity:  capacity,
		supported: supported,
		decayRate: decayRate,
	}
}
