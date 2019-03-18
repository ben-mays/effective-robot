package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
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

// Optionally, can be given an order to use instead of generating one. If an order is not given, one is generated.
func simulateOrder(kitchen *client.Client, orderRequest *server.CreateOrderRequest) *server.OrderResponse {
	resp, err := kitchen.CreateOrder(*orderRequest)
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
			fmt.Printf(color("blue", "%30s\t%8s\t%8s\t%s\t%8s\n"), "Name", "State", "Age", "Value", "Shelf")
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

				fmt.Printf("%30s\t%8s\t%8.2fs\t%s\t%8s\n", o.Name, o.State, o.Age, valueString, o.Shelf)
			}
			fmt.Println()
			spin(count)
			count++
			time.Sleep(time.Millisecond * 100)
		}
	}
}

func run(kitchen *client.Client, numSeconds int, rate float64, staticOrders []server.CreateOrderRequest) {
	// metrics captures each orders' metrics
	metrics := make(chan *server.OrderResponse)
	// done signals that all orders are processed
	done := make(chan bool)

	// launch a background routine to continuously display the kitchen status
	go displayStatus(kitchen, done)

	// generate _rate_ orders, per second, in the main thread. we use a poisson distribution to determine how
	// many orders to create per second.
	orderCount := 0
	dist := distuv.Poisson{Lambda: rate}
	for i := 0; i < numSeconds; i++ {
		orders := int(dist.Rand())
		orderCount += orders

		for j := 0; j < orders; j++ {
			var createOrderReq *server.CreateOrderRequest
			// if no static orders given, generate them randomly
			if len(staticOrders) == 0 {
				name, temp, shelf, decay := makeOrder()
				createOrderReq = &server.CreateOrderRequest{
					Name:      name,
					Temp:      temp,
					ShelfLife: shelf,
					DecayRate: decay,
				}
			} else if orderCount+j < len(staticOrders) {
				createOrderReq = &staticOrders[orderCount+j]
			}
			// no-op if nil. this is useful if the client wants to watch the display but stop creating
			// orders after the file cursor is at eof.
			if createOrderReq != nil {
				go func(req *server.CreateOrderRequest) {
					metrics <- simulateOrder(kitchen, req)
				}(createOrderReq)
			} else {
				// avoid blocking on no-op orders
				orderCount--
			}
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

type orderList []server.CreateOrderRequest

func main() {

	// set defaults
	host := "http://localhost:8080"
	numSeconds := 60
	rate := 3.5
	var orders orderList
	// used to shift pos args when options are given
	shift := 0

	// parse pos args
	if len(os.Args) > 1 {
		if strings.Contains(os.Args[1], "help") {
			fmt.Println("usage: ./runner (options) [hostname] [duration] [orders per second]\noptions:\n\t-f\t A path to a json file containing order definitions.")
			os.Exit(0)
		}
		// handle -f option, shift by 1
		if strings.Contains("-f", os.Args[1]) {
			shift += 2
			bytes, err := ioutil.ReadFile(os.Args[2])
			if err != nil {
				fmt.Printf("invalid file path given: %s", err.Error())
				os.Exit(1)
			}
			err = json.Unmarshal(bytes, &orders)
			if err != nil {
				fmt.Printf("error reading order file: %s\n", err.Error())
				os.Exit(1)
			}
			fmt.Printf("using orders from %s", os.Args[2])
		}
		host = os.Args[shift+1]
		if len(os.Args) > 2 {
			seconds, err := strconv.ParseInt(os.Args[shift+2], 10, 64)
			if err != nil {
				fmt.Printf("invalid duration given: %s", err.Error())
				os.Exit(1)
			}
			numSeconds = int(seconds)
		}
		if len(os.Args) > 3 {
			lambda, err := strconv.ParseFloat(os.Args[shift+3], 64)
			if err != nil {
				fmt.Printf("invalid rate given: %s", err.Error())
				os.Exit(1)
			}
			rate = lambda
		}
	}

	url, err := url.Parse(host)
	if err != nil {
		fmt.Printf("invalid server hostname: %s\n", err.Error())
		os.Exit(1)
	}
	kitchen := &client.Client{
		BaseURL:   url,
		Transport: http.DefaultClient,
	}

	if !kitchen.Healthy() {
		fmt.Printf("cannot reach server: %s\n", url.String())
		os.Exit(1)
	}

	run(kitchen, numSeconds, rate, orders)
}
