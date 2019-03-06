package kitchen

import (
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/config"
)

// Kitchen is the stateful dispatcher and the entry point for other packages. There is only
// a single instance of Kitchen in the application.
type Kitchen struct {
	// shelves are set at app start, these ds are optimizations
	shelvesAsc     []Shelf // shelves from best decay to worse
	shelvesDesc    []Shelf // shelves from worse decay to best
	supportedIndex map[string][]Shelf

	// used for time-travel during testing
	now func() time.Time
}

type kitchenConfig struct {
	RunDecayMinimizer bool          `yaml:"minimize_decay"`
	Topology          []shelfConfig `yaml:"topology"`
}

type shelfConfig struct {
	Name      string   `yaml:"name"`
	Capacity  int      `yaml:"capacity"`
	Supported []string `yaml:"supported"`
	DecayRate float64  `yaml:"decay_rate"`
	Type      string   `yaml:"type"`
}

// optimizePlacement will take an order and a set of shelves, attempting to place an order in an shelf that
// is _atleast_ better with regard to decay.
func (k *Kitchen) optimizePlacement(order *Order, candidates []Shelf) bool {
	// if order is expired, remove it
	if order.IsExpired() {
		order.TransitionOrder(order.State(), Trashed, func(o *Order) error { return nil })
		return false
	}

	currentShelf := order.Shelf()
	orderType := order.Temp()

	// find shelf that supports this type, has capacity
	for _, shelf := range candidates {
		// check supported, as candidates may not be filtered already
		for _, supported := range shelf.Supported() {
			if orderType == supported {
				// avoid trying to replace in current shelf
				if currentShelf != nil && currentShelf == shelf {
					continue
				}

				// if the new shelf is worse or equivalent, skip
				if currentShelf != nil && currentShelf.Decay() <= shelf.Decay() {
					continue
				}

				// try to set new shelf and return if successful
				err := order.SetShelf(shelf)
				if err == nil {
					return true
				}
			}
		}
	}
	return false
}

func (k *Kitchen) decayMinimizer() {
	// Start from worst shelves and try to move orders out.
	// We use a WaitGroup to move each shelf at roughly the same time and to prevent
	// potential liveness issues from constantly taking locks.
	for _, shelf := range k.shelvesDesc {
		wg := sync.WaitGroup{}

		orders := shelf.Orders()
		// Start with the most decayed orders
		sort.Slice(orders, func(i, j int) bool {
			return orders[i].Decayed() > orders[j].Decayed()
		})

		for _, o := range orders {
			wg.Add(1)
			go func(order *Order) {
				defer wg.Done()
				k.optimizePlacement(order, k.shelvesAsc)
			}(o)
		}
		wg.Wait()
	}
}

func loadConfig(provider config.Provider) (kitchenConfig, error) {
	var cfg kitchenConfig
	err := provider.Get("kitchen").Populate(&cfg)
	return cfg, err
}

func buildShelf(cfg shelfConfig) Shelf {
	switch strings.ToLower(cfg.Type) {
	// static is the default type
	case "static":
	default:
		return NewStaticShelf(cfg.Name, cfg.Capacity, cfg.Supported, cfg.DecayRate)
	}
	return nil
}

func buildTopology(cfg kitchenConfig) ([]Shelf, map[string][]Shelf) {
	shelves := make([]Shelf, 0)
	index := make(map[string][]Shelf, 0)
	for _, s := range cfg.Topology {
		shelf := buildShelf(s)
		if shelf == nil {
			continue
		}
		for _, supported := range shelf.Supported() {
			index[supported] = append(index[supported], shelf)
		}
		shelves = append(shelves, shelf)
	}
	return shelves, index
}

func NewKitchen(provider config.Provider) (*Kitchen, error) {
	cfg, err := loadConfig(provider)
	if err != nil {
		return nil, err
	}

	shelves, index := buildTopology(cfg)

	// copy the underlying data into a new slice
	shelvesAsc := make([]Shelf, len(shelves))
	shelvesDesc := make([]Shelf, len(shelves))
	copy(shelvesAsc, shelves)
	copy(shelvesDesc, shelves)

	// sort by decay asc
	sort.Slice(shelvesAsc, func(i, j int) bool {
		return shelvesAsc[i].Decay() < shelvesAsc[j].Decay()
	})

	// sort by decay desc
	sort.Slice(shelvesDesc, func(i, j int) bool {
		return shelvesDesc[i].Decay() > shelvesDesc[j].Decay()
	})

	k := &Kitchen{}
	k.supportedIndex = index
	k.shelvesAsc = shelvesAsc
	k.shelvesDesc = shelvesDesc
	k.now = time.Now

	if cfg.RunDecayMinimizer {
		go func() {
			for {
				k.decayMinimizer()
				// inject jitter
				jitter := time.Duration(rand.Float64()) + time.Second
				time.Sleep(jitter)
			}
		}()
	}

	return k, nil
}

func getOrder(orderID string, shelf Shelf, results chan *Order) {
	order, _ := shelf.Get(orderID)
	results <- order
}

func (k *Kitchen) GetOrder(orderID string) *Order {
	// scatter gather to all shelves
	results := make(chan *Order)
	sent := len(k.shelvesAsc)
	received := 0
	for _, s := range k.shelvesAsc {
		go getOrder(orderID, s, results)
	}
	for {
		select {
		case o := <-results:
			received++
			// if not nil, return fast
			if o != nil {
				return o
			}
		}
		// if all came back nil, return nil
		if received == sent {
			close(results)
			return nil
		}
	}
}

func (k *Kitchen) GetOrders() []*Order {
	orders := make([]*Order, 0)
	for _, shelf := range k.shelvesAsc {
		for _, o := range shelf.Orders() {
			orders = append(orders, o)
		}
	}
	return orders
}

func (k *Kitchen) CreateOrder(order *Order) error {
	// move to order into created state
	order.TransitionOrder("", Created, func(o *Order) error {
		o.createdAt = k.now()
		return nil
	})
	// ... sleep for cook time
	return k.SetOrderReady(order)
}

func (k *Kitchen) SetOrderReady(order *Order) error {
	supported, exists := k.supportedIndex[order.Temp()]
	if !exists {
		order.TransitionOrder(Created, Trashed, func(o *Order) error {
			o.state = Trashed
			o.trashedAt = k.now()
			removeOrder(order)
			return nil
		})
		return errors.New("no shelves available for this order type")
	}

	// sort by decay
	sort.Slice(supported, func(i, j int) bool {
		return supported[i].Decay() < supported[j].Decay()
	})

	// try to place on a shelf
	if k.optimizePlacement(order, supported) {
		order.TransitionOrder(Created, Ready, func(o *Order) error {
			o.readyAt = k.now()
			return nil
		})
		return nil
	}

	// log not placed, discard
	order.TransitionOrder(Created, Trashed, func(o *Order) error {
		o.trashedAt = k.now()
		removeOrder(order)
		return nil
	})

	return errors.New("failed to place order on a valid shelf")
}

func (k *Kitchen) SetOrderEnroute(order *Order) error {
	return order.TransitionOrder(Ready, Enroute, func(o *Order) error {
		o.enrouteAt = k.now()
		return nil
	})
}

func (k *Kitchen) SetOrderPickedUp(order *Order) error {
	return order.TransitionOrder(Enroute, PickedUp, func(o *Order) error {
		o.pickedUpAt = k.now()
		removeOrder(order)
		return nil
	})
}
