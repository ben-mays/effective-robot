package kitchen

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// OrderState is a small set of states that make up a simple state machine.
type OrderState string

const (
	Created OrderState = "created"
	Ready   OrderState = "ready"
	Enroute OrderState = "enroute"

	// Terminal states
	PickedUp OrderState = "pickedup"
	Trashed  OrderState = "trashed"
)

type OrderEvent struct {
	OrderID  string
	OldState OrderState
	NewState OrderState
}

type OrderRecord struct {
	PlacedAt time.Time
	Shelf    Shelf
}

// Order is the basic primitive representing a incoming order from a customer.
type Order struct {
	sync.RWMutex

	id   string
	name string
	temp string

	// ShelfLife is the max shelf time for an order
	shelfLife time.Duration

	// BaseDecayRate is the rate of decay per second
	baseDecayRate float64
	state         OrderState

	// track previous decayed amount from older shelves
	prevDecayed float64

	// Store timestamps for each state
	createdAt  time.Time
	readyAt    time.Time
	enrouteAt  time.Time
	pickedUpAt time.Time
	trashedAt  time.Time

	// Keep a pointer to current shelf
	shelf    Shelf
	placedAt time.Time

	// used for time-travel during testing
	now func() time.Time
}

func NewOrder(
	name string,
	temp string,
	shelfLife time.Duration,
	decayRate float64,
) *Order {
	o := &Order{
		id:            uuid.New().String(),
		name:          name,
		temp:          temp,
		shelfLife:     shelfLife,
		baseDecayRate: decayRate,
		now:           time.Now,
	}
	return o
}

func (order *Order) ID() string {
	return order.id
}

func (order *Order) Name() string {
	return order.name
}

func (order *Order) Temp() string {
	return order.temp
}

func (order *Order) ShelfLife() time.Duration {
	return order.shelfLife
}

func (order *Order) DecayRate() float64 {
	return order.baseDecayRate
}

func (order *Order) State() OrderState {
	order.RLock()
	defer order.RUnlock()
	return order.state
}

// Shelf returns the reference to the Shelf instance this Order belongs to.
func (order *Order) Shelf() Shelf {
	order.RLock()
	defer order.RUnlock()
	return order.shelf
}

// Age is the duration that has elapsed since the order entered the Ready state.
func (order *Order) Age() time.Duration {
	order.RLock()
	defer order.RUnlock()
	return order.age()
}

// unsafe age function
func (order *Order) age() time.Duration {
	t := order.now()
	switch order.state {
	case PickedUp:
		t = order.pickedUpAt
	case Trashed:
		return 0
	}
	return t.Sub(order.readyAt)
}

// RawValue is the value for the Order, not including Decay.
func (order *Order) RawValue() float64 {
	order.RLock()
	defer order.RUnlock()
	return order.rawValue()
}

// unsafe rawValue
func (order *Order) rawValue() float64 {
	switch order.state {
	case "", Created, Trashed:
		return 0
	}
	return float64(order.shelfLife - order.age())
}

// Value represents the _real_ value of the order at the current age. Decay
// is calculated based on the order's shelf history in the Kitchen.
func (order *Order) Value() float64 {
	order.RLock()
	defer order.RUnlock()
	return order.value()
}

// unsafe value
func (order *Order) value() float64 {
	return order.rawValue() - order.decayed()
}

// NormalizedValue is the value over the shelflife.
func (order *Order) NormalizedValue() float64 {
	order.RLock()
	defer order.RUnlock()
	return order.value() / float64(order.shelfLife)
}

// IsExpired returns true when the order is expired, meaning that the value is less than zero.
func (order *Order) IsExpired() bool {
	order.RLock()
	defer order.RUnlock()
	return order.isExpired()
}

// unsafe isExpired
func (order *Order) isExpired() bool {
	switch order.state {
	case "", Created, PickedUp, Trashed:
		return false
	}
	// decayed represents total decay amount, including previous shelves
	return order.value() <= 0
}

func (order *Order) Decayed() float64 {
	order.RLock()
	defer order.RUnlock()
	return order.decayed()
}

// unsafe decayed
func (order *Order) decayed() float64 {
	// if there is an existing shelf (and the order is still active), calc running decay
	var decay float64
	if order.shelf != nil {
		t := order.now()
		if order.state == PickedUp {
			t = order.pickedUpAt
		}
		timeAt := t.Sub(order.placedAt)
		decay = order.shelf.Decay() * float64(timeAt)
	}

	// add base decay
	decay += order.baseDecayRate * float64(order.age())
	// decayed represents total decay amount, including previous shelves
	return order.prevDecayed + decay
}

// SetShelf updates the current shelf of the Order and pushes a OrderRecord on the history.
func (order *Order) SetShelf(shelf Shelf) error {
	order.Lock()
	defer order.Unlock()

	err := shelf.Put(order)
	if err != nil {
		return err
	}

	// if there is an existing shelf, update the running decay and remove the order from it
	removeOrder(order)

	// update shelf meta
	order.shelf = shelf
	order.placedAt = order.now()
	return nil
}

// Helper function. removeOrder must be called by a function that is holding the lock for this order.
func removeOrder(order *Order) {
	if order.shelf != nil {
		timeAt := order.now().Sub(order.placedAt)
		decay := order.shelf.Decay() * float64(timeAt)
		order.prevDecayed += decay
		order.shelf.Remove(order.ID())
		order.shelf = nil
	}
}

// TransitionOrder will update the Order to the given newState iff the current state is equal to the expectedState.
func (order *Order) TransitionOrder(
	expectedState OrderState,
	newState OrderState,
	sideEffect func(*Order) error,
) error {
	order.Lock()
	defer order.Unlock()
	if order.state != expectedState {
		return fmt.Errorf("order %s in incorrect state %s, expected %s", order.id, order.state, expectedState)
	}

	switch order.state {
	case PickedUp, Trashed:
		return fmt.Errorf("order %s was in terminal state %s, invalid transition", order.id, order.state)
	}

	// double check the value here and hijack the transition if the value is negative
	if order.isExpired() {
		order.state = Trashed
		order.trashedAt = order.now()
		removeOrder(order)
		return fmt.Errorf("order %s expired", order.id)
	}

	order.state = newState
	err := sideEffect(order)
	if err != nil {
		return err
	}

	return nil
}
