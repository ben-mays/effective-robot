package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ben-mays/effective-robot/server"
	"go.uber.org/config"
)

type ClientConfig struct {
	Host string `yaml:"url"`
}

type Client struct {
	BaseURL *url.URL

	Transport *http.Client
}

// LoadConfig returns a valid Client instacne using the default http.Client.
func LoadConfig(provider config.Provider) (*Client, error) {
	var cfg ClientConfig
	provider.Get("client").Populate(&cfg)
	host, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, err
	}

	return &Client{
		BaseURL:   host,
		Transport: http.DefaultClient,
	}, nil
}

func (c Client) Healthy() bool {
	resp, err := c.Transport.Get(c.BaseURL.String() + "/health")
	if err != nil {
		return false
	}
	return resp.StatusCode == 200
}

func (c Client) CreateOrder(req server.CreateOrderRequest) (*server.CreateOrderResponse, error) {
	var response server.CreateOrderResponse
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	uri := c.BaseURL.String() + "/order"
	resp, err := c.Transport.Post(uri, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return nil, err
	}
	return &response, err
}

func (c *Client) GetOrder(orderID string) (*server.OrderResponse, error) {
	var order server.OrderResponse
	uri := c.BaseURL.String() + fmt.Sprintf("/order/%s", orderID)
	resp, err := c.Transport.Get(uri)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, errors.New("order not found")
	}
	err = json.NewDecoder(resp.Body).Decode(&order)
	if err != nil {
		return nil, err
	}
	return &order, err
}

func (c *Client) ListOrders() (*server.ListOrdersResponse, error) {
	var orders server.ListOrdersResponse
	uri := c.BaseURL.String() + fmt.Sprintf("/order")
	resp, err := c.Transport.Get(uri)
	if err != nil {
		return nil, err
	}
	err = json.NewDecoder(resp.Body).Decode(&orders)
	if err != nil {
		return nil, err
	}
	return &orders, err
}

func (c *Client) UpdateOrder(orderID string, req server.UpdateOrderRequest) (*server.OrderResponse, error) {
	var order server.OrderResponse
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	uri := c.BaseURL.String() + fmt.Sprintf("/order/%s", orderID)
	resp, err := c.Transport.Post(uri, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, errors.New("Update order failed")
	}
	err = json.NewDecoder(resp.Body).Decode(&order)
	if err != nil {
		return nil, err
	}
	return &order, nil
}
