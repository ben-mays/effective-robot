package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"time"

	"github.com/ben-mays/effective-robot/client"
	"github.com/ben-mays/effective-robot/server"
	"gonum.org/v1/gonum/stat/distuv"
)

func makeOrder() (string, string, float64, float64) {
	foods := []struct {
		name      string
		temp      string
		shelflife float64
		decay     float64
	}{
		{
			name:      "icecream",
			temp:      "cold",
			shelflife: 25,
			decay:     1,
		},
		{
			name:      "soup",
			temp:      "hot",
			shelflife: 50,
			decay:     1,
		},
		{
			name:      "pizza",
			temp:      "frozen",
			shelflife: 100,
			decay:     1,
		},
	}
	choice := rand.Intn(len(foods))
	food := foods[choice]
	return food.name, food.temp, food.shelflife, food.decay
}

func simulateOrder(kitchen *client.Client) *server.OrderResponse {
	name, temp, shelf, decay := makeOrder()
	resp, err := kitchen.CreateOrder(server.CreateOrderRequest{
		Name:      name,
		Temp:      temp,
		ShelfLife: shelf,
		DecayRate: decay,
	})
	if err != nil {
		return nil
	}
	// TODO: add dispatch time
	order, err := kitchen.UpdateOrder(resp.OrderID, server.UpdateOrderRequest{
		State: "enroute",
	})
	if err != nil {
		return nil
	}
	sleep := (rand.Int() + 2) % 10 // get random duration in seconds
	time.Sleep(time.Duration(sleep) * time.Second)
	order, err = kitchen.UpdateOrder(resp.OrderID, server.UpdateOrderRequest{
		State: "pickedup",
	})
	if err != nil {
		return nil
	}
	return order
}

func clear() {
	cmd := exec.Command("clear")
	cmd.Stdout = os.Stdout
	cmd.Run()
}

var spinner = []string{
	"⠋",
	"⠙",
	"⠚",
	"⠞",
	"⠖",
	"⠦",
	"⠴",
	"⠲",
	"⠳",
	"⠓",
}

func spin(pos int) {
	idx := pos % len(spinner)
	fmt.Println(color("blue", spinner[idx]))
}

func color(color, formatString string) string {
	var on string
	off := "\033[0m"
	switch color {
	case "red":
		on = "\033[0;31m"
	case "green":
		on = "\033[0;32m"
	case "blue":
		on = "\033[0;34m"
	case "yellow":
		on = "\033[1;33m"
	default:
		return formatString
	}
	return on + formatString + off
}

func displayStatus(kitchen *client.Client, done chan bool) {
	count := 0
	for {
		select {
		case <-done:
			return
		default:
			resp, err := kitchen.ListOrders()
			if err != nil {
				continue
			}
			clear()
			fmt.Printf(color("blue", "%8s\t%8s\t%8s\t%s\t%8s\n"), "Name", "State", "Age", "Value", "Shelf")
			sort.Slice(resp.Orders, func(i, j int) bool {
				if resp.Orders[i].NormalValue == resp.Orders[j].NormalValue {
					// sort by age if equal
					return resp.Orders[i].Age < resp.Orders[j].Age
				}
				return resp.Orders[i].NormalValue < resp.Orders[j].NormalValue
			})
			for _, o := range resp.Orders {

				valueString := fmt.Sprintf("%.2f", o.NormalValue)
				if o.NormalValue > .50 {
					valueString = color("green", valueString)
				} else if o.NormalValue < .25 {
					valueString = color("red", valueString)
				} else {
					valueString = color("yellow", valueString)
				}

				fmt.Printf("%8s\t%8s\t%8.2fs\t%s\t%8s\n", o.Name, o.State, o.Age, valueString, o.Shelf)
			}
			fmt.Println()
			spin(count)
			count++
			time.Sleep(time.Millisecond * 100)
		}
	}
}

func run(kitchen *client.Client, numSeconds int, rate float64) {

	// metrics captures each orders' metrics
	metrics := make(chan *server.OrderResponse)
	// done signals that all orders are processed
	done := make(chan bool)

	// launch a background routine to continuously display the kitchen
	// status
	go displayStatus(kitchen, done)

	// generate $rate orders, per second, in the main thread
	orderCount := 0
	dist := distuv.Poisson{Lambda: rate}
	for i := 0; i < numSeconds; i++ {
		orders := int(dist.Rand())
		orderCount += orders
		for j := 0; j < orders; j++ {
			go func() {
				metrics <- simulateOrder(kitchen)
			}()
		}
		time.Sleep(time.Second)
	}

	// agg metrics
	counts := map[string]int{
		"trashed":  0,
		"pickedup": 0,
	}
	failed := 0
	sumDecay := 0.0
	sumValue := 0.0
	sumNorm := 0.0
	received := 0

	for received < orderCount {
		select {
		case o := <-metrics:
			received++
			if o == nil {
				failed++
				continue
			}

			sumDecay += o.Decay
			sumValue += o.Value
			sumNorm += o.NormalValue
			counts[o.State]++
		}
	}

	// signal done
	done <- true
	close(metrics)

	// print stat
	clear()
	fmt.Printf("Stats:\n  Generated %d orders, failed %d.\n  Avg/sec: %.2f\n  Avg value: %.2f\n  Total Value: %.2f\n  Avg normalized value: %.2f\n  Avg decay: %.2f\n  SuccessPerc: %.2f\n  PickedUp: %d\n  Trashed: %d\n\n",
		orderCount,
		failed,
		float64(orderCount)/float64(numSeconds),
		sumValue/float64(orderCount),
		sumValue,
		sumNorm/float64(orderCount),
		sumDecay/float64(orderCount),
		float64(counts["pickedup"])/float64(orderCount),
		counts["pickedup"],
		counts["trashed"])
}

func main() {

	// set defaults
	host := "http://localhost:8080"
	numSeconds := 60
	rate := 3.5

	// parse pos args
	if len(os.Args) > 1 {
		if os.Args[1] == "help" {
			fmt.Println("usage: ./runner [server host] [time to run] [lambda]")
			os.Exit(0)
		}
		host = os.Args[1]
		if len(os.Args) > 2 {
			seconds, err := strconv.ParseInt(os.Args[2], 10, 64)
			if err != nil {
				os.Exit(1)
			}
			numSeconds = int(seconds)
		}
		if len(os.Args) > 3 {
			lambda, err := strconv.ParseFloat(os.Args[3], 64)
			if err != nil {
				os.Exit(1)
			}
			rate = lambda
		}
	}

	url, err := url.Parse(host)
	if err != nil {
		panic(err)
	}
	kitchen := &client.Client{
		BaseURL:   url,
		Transport: http.DefaultClient,
	}

	if !kitchen.Healthy() {
		panic(fmt.Sprintf("cannot reach server: %s", url.String()))
	}

	run(kitchen, numSeconds, rate)
}
