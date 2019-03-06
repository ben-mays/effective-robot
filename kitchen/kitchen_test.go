package kitchen

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"go.uber.org/config"
)

var simpleConfig = []byte(`
kitchen:
  topology:
    - name: "hot"
      capacity: 1
      decay_rate: 1
      supported: 
        - hot
    - name: "cold"
      capacity: 1
      decay_rate: 0.5
      supported: 
        - cold`)

func TestKitchenConstructor(t *testing.T) {
	provider := config.NewYAMLProviderFromBytes(simpleConfig)
	k, err := NewKitchen(provider)
	assert.Nil(t, err)
	assert.NotNil(t, k)

	assert.NotNil(t, k.shelvesAsc)
	assert.NotNil(t, k.shelvesDesc)
	assert.NotNil(t, k.supportedIndex)

	// assert topology matches, sorted by decay
	assert.Equal(t, 2, len(k.shelvesAsc))
	assert.Equal(t, 2, len(k.shelvesDesc))

	// assert ascend/desc arrays are correct
	assert.Equal(t, "cold", k.shelvesAsc[0].Name())
	assert.Equal(t, 1, k.shelvesAsc[0].Capacity())
	assert.Equal(t, .5, k.shelvesAsc[0].Decay())

	assert.Equal(t, "hot", k.shelvesAsc[1].Name())
	assert.Equal(t, 1, k.shelvesAsc[1].Capacity())
	assert.Equal(t, 1.0, k.shelvesAsc[1].Decay())

	assert.Equal(t, "cold", k.shelvesDesc[1].Name())
	assert.Equal(t, 1, k.shelvesDesc[1].Capacity())
	assert.Equal(t, .5, k.shelvesDesc[1].Decay())

	assert.Equal(t, "hot", k.shelvesDesc[0].Name())
	assert.Equal(t, 1, k.shelvesDesc[0].Capacity())
	assert.Equal(t, 1.0, k.shelvesDesc[0].Decay())

	// assert index is correct
	assert.Equal(t, []Shelf{k.shelvesAsc[0]}, k.supportedIndex["cold"])
	assert.Equal(t, []Shelf{k.shelvesAsc[1]}, k.supportedIndex["hot"])
}

func TestKitchenPlacement(t *testing.T) {
	top := []byte(`--- 
kitchen: 
  topology: 
    - capacity: 1
      decay_rate: 1
      name: bad
      supported: 
        - hot
    - capacity: 1
      decay_rate: 0.5
      name: good
      supported: 
        - hot
    - capacity: 1
      decay_rate: 0
      name: best
      supported: 
        - hot`)
	provider := config.NewYAMLProviderFromBytes(top)
	k, err := NewKitchen(provider)
	assert.Nil(t, err)

	orders := []*Order{
		NewOrder("test1", "hot", 100*time.Second, .2),
		NewOrder("test2", "hot", 100*time.Second, .2),
		NewOrder("test3", "hot", 100*time.Second, .2),
	}
	// move into shelves
	for _, o := range orders {
		k.CreateOrder(o)
		k.SetOrderReady(o)
	}

	// assert that test1 went to best, test2 to good and test3 to bad ..
	assert.Equal(t, "test1", orders[0].Name())
	assert.Equal(t, "best", orders[0].Shelf().Name())

	assert.Equal(t, "test2", orders[1].Name())
	assert.Equal(t, "good", orders[1].Shelf().Name())

	assert.Equal(t, "test3", orders[2].Name())
	assert.Equal(t, "bad", orders[2].Shelf().Name())

	// pop test1 and call optimize
	k.SetOrderEnroute(orders[0])
	k.SetOrderPickedUp(orders[0])
	assert.True(t, k.optimizePlacement(orders[1], k.shelvesAsc))
	assert.True(t, k.optimizePlacement(orders[2], k.shelvesAsc))

	// Now test2 should be in best, test3 in good
	assert.Equal(t, "test1", orders[0].Name())
	assert.Nil(t, orders[0].Shelf())

	assert.Equal(t, "test2", orders[1].Name())
	assert.Equal(t, "best", orders[1].Shelf().Name())

	assert.Equal(t, "test3", orders[2].Name())
	assert.Equal(t, "good", orders[2].Shelf().Name())
}

func TestOrderExpireBackground(t *testing.T) {
	cfg := []byte(`
        kitchen:
          minimize_decay: false
          topology:
            - name: "hot"
              capacity: 150
              decay_rate: 1
              supported: 
                - hot
            - name: "cold"
              capacity: 150
              decay_rate: 0.5
              supported: 
                - cold`)

	provider := config.NewYAMLProviderFromBytes(cfg)
	k, err := NewKitchen(provider)
	assert.Nil(t, err)

	order := NewOrder("test1", "hot", 1*time.Minute, .2)
	k.CreateOrder(order)
	k.SetOrderReady(order)
	assert.Equal(t, Ready, order.State())

	// time travel by 10 minutes
	nowPlus := func() time.Time {
		return time.Now().Add(10 * time.Minute)
	}

	k.now = nowPlus
	order.now = nowPlus

	// trigger the background routine manually and wait for it to finish
	k.decayMinimizer()

	// assert that test1 is expired
	assert.Equal(t, "test1", order.Name())
	assert.Equal(t, Trashed, order.State())
	assert.True(t, 0 >= order.Value())
	assert.Nil(t, order.Shelf())
}

func makeOrders(count int, orderType string) []*Order {
	orders := make([]*Order, count)
	for i := 0; i < count; i++ {
		orders[i] = NewOrder(fmt.Sprintf("test_%d", count), orderType, 1*time.Second, .2)
	}
	return orders
}

func TestKitchenCapacity(t *testing.T) {
	cfg := []byte(`
        kitchen:
          minimize_decay: false
          topology:
            - name: "hot"
              capacity: 5
              decay_rate: 1
              supported: 
                - hot
            - name: "cold"
              capacity: 5
              decay_rate: 0.5
              supported: 
                - cold`)

	provider := config.NewYAMLProviderFromBytes(cfg)
	k, err := NewKitchen(provider)
	assert.NotNil(t, k)
	assert.Nil(t, err)

	orders := makeOrders(6, "hot")

	// populate kitchen with 5 orders
	for i := 0; i < len(orders)-1; i++ {
		k.CreateOrder(orders[i])
		k.SetOrderReady(orders[i])
		assert.Equal(t, Ready, orders[i].State())
	}

	k.CreateOrder(orders[len(orders)-1])
	k.SetOrderReady(orders[len(orders)-1])

	// assert that last order is trashed is expired
	assert.Equal(t, "test_6", orders[len(orders)-1].Name())
	assert.Equal(t, Trashed, orders[len(orders)-1].State())
	assert.True(t, 0 >= orders[len(orders)-1].Value())
	assert.Nil(t, orders[len(orders)-1].Shelf())
}

func TestKitchenUnsupported(t *testing.T) {
	// topology only has hot or cold shelves
	provider := config.NewYAMLProviderFromBytes(simpleConfig)
	k, err := NewKitchen(provider)
	assert.NotNil(t, k)
	assert.Nil(t, err)

	orders := makeOrders(5, "frozen")

	// populate kitchen with 5 orders that are unsupported
	for i := 0; i < len(orders)-1; i++ {
		k.CreateOrder(orders[i])
		k.SetOrderReady(orders[i])
		// they get trashed since there is no shelf for them
		assert.Equal(t, Trashed, orders[i].State())
		assert.True(t, 0 >= orders[i].Value())
		assert.Nil(t, orders[i].Shelf())
	}
}

func setupKitchen(cfg []byte, types []string, numOrders int, expiry time.Duration) ([]*Order, *Kitchen) {
	provider := config.NewYAMLProviderFromBytes(cfg)
	k, _ := NewKitchen(provider)
	rand.Seed(1)
	orders := make([]*Order, numOrders)
	for i := 0; i < numOrders; i++ {
		r := rand.Float64()
		orderType := types[int(r)*len(types)]
		if expiry == 0 {
			expiry = time.Duration(rand.Intn(15)) * time.Second
		}
		order := NewOrder(fmt.Sprintf("bench_%d", i), orderType, expiry, rand.Float64())
		orders[i] = order
		k.CreateOrder(order)
	}
	return orders, k
}

func TestManyOrders(t *testing.T) {
	cfg := []byte(`
        kitchen:
          minimize_decay: true
          topology:
            - name: "storage"
              capacity: 15
              decay_rate: 2
              supported: 
                - cold
                - hot
            - name: "hot"
              capacity: 15
              decay_rate: 1
              supported: 
                - hot
            - name: "cold"
              capacity: 15
              decay_rate: 0.5
              supported: 
                - cold`)
	orders, k := setupKitchen(cfg, []string{"cold", "hot"}, 30, time.Second*30)
	wg := sync.WaitGroup{}
	for _, order := range orders {
		wg.Add(1)
		go func(o *Order) {
			defer wg.Done()
			sleep := time.Second * time.Duration(rand.Intn(10))
			k.SetOrderReady(o)
			k.SetOrderEnroute(o)
			time.Sleep(sleep)
			k.SetOrderPickedUp(o)
		}(order)
	}
	wg.Wait()

	values := make([]float64, len(orders))
	rawValues := make([]float64, len(orders))
	normalValues := make([]float64, len(orders))
	decayValues := make([]float64, len(orders))
	counts := map[OrderState]int{
		Created:  0,
		Ready:    0,
		Enroute:  0,
		PickedUp: 0,
		Trashed:  0,
	}
	for i, order := range orders {
		values[i] = order.Value()
		rawValues[i] = order.RawValue()
		normalValues[i] = order.NormalizedValue()
		decayValues[i] = order.Decayed()
		counts[order.State()]++
	}

	// assert that all orders completed
	assert.Equal(t, len(orders), counts[PickedUp])
	assert.Equal(t, 0, counts[Trashed])
}

func BenchmarkOrders(b *testing.B) {
	cfg := []byte(`
        kitchen:
          minimize_decay: true
          topology:
            - name: "storage"
              capacity: 1500
              decay_rate: 2
              supported: 
                - cold
                - hot
                - frozen
            - name: "hot"
              capacity: 400
              decay_rate: 1
              supported: 
                - hot
            - name: "cold"
              capacity: 400
              decay_rate: 0.5
              supported: 
                - cold`)
	orders, k := setupKitchen(cfg, []string{"cold", "hot", "frozen"}, 2000, 0)
	for _, o := range orders {
		k.CreateOrder(o)
		k.SetOrderReady(o)
	}
	for n := 0; n < b.N; n++ {
		k.decayMinimizer()
	}
}

// Benchmark scatter-gather GetOrder implementation.

func BenchmarkGetOrder(b *testing.B) {
	cfg := []byte(`
        kitchen:
          minimize_decay: false
          topology:
            - name: "test1"
              capacity: 5
              decay_rate: 1
              supported: 
                - test1
            - name: "test2"
              capacity: 5
              decay_rate: 1
              supported: 
                - test2
            - name: "test3"
              capacity: 5
              decay_rate: 1
              supported: 
                - test3`)
	orders, k := setupKitchen(cfg, []string{"test1", "test2", "test3"}, 30, time.Hour)
	for _, o := range orders {
		k.CreateOrder(o)
		k.SetOrderReady(o)
	}
	id := orders[0].ID()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		k.GetOrder(id)
	}
}

// Benchmark scatter-gather GetOrder implementation when many running in parallel.
func BenchmarkGetOrderContention(b *testing.B) {
	cfg := []byte(`
    kitchen:
      minimize_decay: false
      topology:
        - name: "test1"
          capacity: 5
          decay_rate: 1
          supported: 
            - test1
        - name: "test2"
          capacity: 5
          decay_rate: 1
          supported: 
            - test2
        - name: "test3"
          capacity: 5
          decay_rate: 1
          supported: 
            - test3`)
	orders, k := setupKitchen(cfg, []string{"test1", "test2", "test3"}, 30, time.Hour)
	for _, o := range orders {
		k.CreateOrder(o)
		k.SetOrderReady(o)
	}
	id := orders[0].ID()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			k.GetOrder(id)
		}
	})
}

func BenchmarkCreateOrderContention(b *testing.B) {
	cfg := []byte(`
    kitchen:
      minimize_decay: false
      topology:
        - name: "test1"
          capacity: 5
          decay_rate: 1
          supported: 
            - test1
        - name: "test2"
          capacity: 5
          decay_rate: 1
          supported: 
            - test2
        - name: "test3"
          capacity: 5
          decay_rate: 1
          supported: 
            - test3`)
	orders, k := setupKitchen(cfg, []string{"test1", "test2", "test3"}, 30, time.Hour)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			o := orders[rand.Intn(30)]
			k.CreateOrder(o)
			k.SetOrderReady(o)
		}
	})
}
