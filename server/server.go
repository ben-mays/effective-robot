package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ben-mays/effective-robot/kitchen"
	"github.com/gorilla/mux"
	"go.uber.org/config"
	"go.uber.org/fx"
)

type ApplicationServer struct {
	router  *mux.Router
	server  *http.Server
	kitchen *kitchen.Kitchen
	port    int
}

func (s *ApplicationServer) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("✔"))
}

type ListOrdersResponse struct {
	Orders []OrderResponse `json:"orders"`
}

func (s *ApplicationServer) ListOrdersHandler(w http.ResponseWriter, r *http.Request) {
	orders := s.kitchen.GetOrders()
	var res ListOrdersResponse
	res.Orders = make([]OrderResponse, len(orders))
	for i, order := range orders {
		orderResp := orderToOrderResponse(order)
		res.Orders[i] = orderResp
	}
	bytes, err := json.Marshal(res)
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}
	w.Write([]byte(bytes))
}

type CreateOrderRequest struct {
	Name      string  `json:"name"`
	Temp      string  `json:"temp"`
	ShelfLife float64 `json:"shelfLife"`
	DecayRate float64 `json:"decayRate"`
}

type CreateOrderResponse struct {
	OrderID string `json:"orderID"`
}

func (s *ApplicationServer) CreateOrderHandler(w http.ResponseWriter, r *http.Request) {
	var req CreateOrderRequest
	var res CreateOrderResponse

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		w.WriteHeader(400)
		return
	}
	order := kitchen.NewOrder(req.Name, req.Temp, time.Duration(req.ShelfLife)*time.Second, req.DecayRate)
	err = s.kitchen.CreateOrder(order)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	res.OrderID = order.ID()
	bytes, err := json.Marshal(res)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	w.Write(bytes)
}

type UpdateOrderRequest struct {
	State string `json:"state"`
}

func (s *ApplicationServer) UpdateOrderHandler(w http.ResponseWriter, r *http.Request) {
	var req UpdateOrderRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		w.WriteHeader(400)
		return
	}
	id := mux.Vars(r)["id"]
	order := s.kitchen.GetOrder(id)
	if order == nil {
		w.WriteHeader(404)
		return
	}
	if strings.ToLower(req.State) == "ready" {
		err = s.kitchen.SetOrderReady(order)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		writeOrderResponse(w, order)
		return
	}
	if strings.ToLower(req.State) == "enroute" {
		err = s.kitchen.SetOrderEnroute(order)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		writeOrderResponse(w, order)
		return
	}
	if strings.ToLower(req.State) == "pickedup" {
		err = s.kitchen.SetOrderPickedUp(order)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		writeOrderResponse(w, order)
		return
	}
}

type OrderResponse struct {
	OrderID     string  `json:"orderID"`
	Name        string  `json:"name"`
	ShelfLife   float64 `json:"shelfLife"`
	State       string  `json:"state"`
	Shelf       string  `json:"shelf"`
	Value       float64 `json:"value"`
	NormalValue float64 `json:"normal"`
	Decay       float64 `json:"decay"`
	Age         float64 `json:"age"`
}

func orderToOrderResponse(order *kitchen.Order) OrderResponse {
	var shelfName string
	if shelf := order.Shelf(); shelf != nil {
		shelfName = shelf.Name()
	}
	// We convert from internal time.Duration here to seconds.
	return OrderResponse{
		OrderID:     order.ID(),
		Name:        order.Name(),
		State:       string(order.State()),
		Shelf:       shelfName,
		ShelfLife:   float64(order.ShelfLife() / time.Second),
		Value:       order.Value() / float64(time.Second),
		NormalValue: order.NormalizedValue(),
		Decay:       order.Decayed() / float64(time.Second),
		Age:         float64(order.Age() / time.Second),
	}
}

func writeOrderResponse(w http.ResponseWriter, order *kitchen.Order) {
	res := orderToOrderResponse(order)
	bytes, err := json.Marshal(res)
	if err != nil {
		w.WriteHeader(500)
	}
	w.Write([]byte(bytes))
}

func (s *ApplicationServer) GetOrderHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	order := s.kitchen.GetOrder(id)
	if order == nil {
		w.WriteHeader(404)
		return
	}
	res := orderToOrderResponse(order)
	bytes, err := json.Marshal(res)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	w.Write([]byte(bytes))
}

type Config struct {
	Port int `yaml:"port"`
}

// allow zero values and set defaults
func loadConfig(provider config.Provider) Config {
	var cfg Config
	provider.Get("server").Populate(&cfg)
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	return cfg
}

func Provide(provider config.Provider, k *kitchen.Kitchen) (*ApplicationServer, error) {
	cfg := loadConfig(provider)
	app := ApplicationServer{kitchen: k, port: cfg.Port}
	app.router = mux.NewRouter()
	app.router.HandleFunc("/order", app.CreateOrderHandler).Methods("POST")
	app.router.HandleFunc("/order", app.ListOrdersHandler).Methods("GET")
	app.router.HandleFunc("/order/{id}", app.GetOrderHandler).Methods("GET")
	app.router.HandleFunc("/order/{id}", app.UpdateOrderHandler).Methods("POST")
	app.router.HandleFunc("/health", app.HealthHandler).Methods("GET")
	app.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", cfg.Port),
		Handler: app.router,
	}
	return &app, nil
}

func Start(lifecycle fx.Lifecycle, server *ApplicationServer) error {
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go server.server.ListenAndServe()
			fmt.Printf("Server listening on %d\n", server.port)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return server.server.Shutdown(ctx)
		},
	})
	return nil
}
