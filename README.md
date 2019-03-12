# effective-robot

The name is generated by GitHub.

TL;DR everything is dockerized and executed by `make`

* Run: `make run` - launches a container with the server running on the hostport 8080
* Test: `make test` - runs all tests
* Challenge: `make challenge` - launches two containers, one for the server and one for the challenge runner. See below for an example run.


[![asciicast](https://asciinema.org/a/tMlSYzPE85eGI3dlZe7upi1Ks.svg)](https://asciinema.org/a/tMlSYzPE85eGI3dlZe7upi1Ks)


There are 3 exported packages:

* A basic client library:`github.com/ben-mays/effective-robot/client`.
* The API server: `github.com/ben-mays/effective-robot/server`
* The kitchen service: `github.com/ben-mays/effective-robot/kitchen`

Additionally, `runner` contains the code for executing the challenge.

You can configure the server, and client, by modifying configuration files under `config/`. The configuration file loaded is determined by the enviornment variable `SERVICE_ENV`. If no environment is set, the default is `development` (e.g. the default is `config/development.yaml`). 

An example configuratiom:

```yaml
# config/development.yaml

server:
  port: 8080

client:
  url: localhost:8080

kitchen:
  minimize_decay: true
  topology:
    ... # see Topology section below

```

## Challenge ##

The design has 3 components:

*Order*

A thread-safe value object representing a _Order_, with a state machine implementation. The state machine exposes a TransitionOrder method that will enforce state changes and allows callers to set a side effect to be executed on the order _after_ the transition is successful.
 
The Order state machine is super simple:

    created -> ready -> enroute -> picked up

And there is a terminal failure state (`trashed`) if an order expires.

*Kitchen* 

The stateful order controller, which is responsible for driving the Order state machine, optimizing the order placements. 

*Shelves* 

A thread-safe container interface for storing Orders. A shelf has a decay rate that is additive to the total decay of an order.

---

Internally, these 3 entity relationships are modeled as the following:

* The Kitchen has many Shelves
* A Shelf has many Orders
* A Order has 1 Shelf  (redundant, but an optimization for calc decay)

Initially I started with a simpler model but found that calculating an Order's decay required knowing the decay of the existing shelf, while also holding a lock for the Order. I wanted to keep the _row_ level lock on each Order entity and not complicate the Kitchen further, so opted to added a reference on the Order to it's current shelf. Another approach I explored was storing a history table for all shelf movement, and then using that to calculate the decay for any order at a given time. This was _cool_ but overly-complicated and removed.

The Kitchen should be a singleton, as it runs a background process that continuously tries to optimize the order placement. The optimization algorithm is dead simple:

    * For all shelves S, sorted from worst to best decay rates
    * For all existing orders O in shelf S, 
        Place O in S+1 iff S+1 has a lower decay rate than S and supports O order type

This executes in a tight loop, with a jitter-ed sleep to prevent thundering herd behavior. The placement algorithm simply walks each shelf, taking the shelf lock and attempting to place the order. If it's successful, we return for that order, else we continue. This algorithm optimizes for moving the most number of orders off the worst shelves- not moving the lowest value orders to the best shelf.

I did not implement persistence here, everything is in-memory. If I did, I would move a lot of the collision checking into the database using OCC on row-level version attributes, to support multiple instances of the application server working on the same shelves.

### Shelf Topology ###

The challenge has a fixed topology, with a simple decay calculation. In this service, you can provide configuration for custom topologies, which supports a much more complex decay calculation. Because an order may move across many shelves, this turns the decay calculation into:

```python
base_decay = order_base_decay_rate * order_age

for shelf in shelves:
  shelf_decay += time_at * shelf.decay_rate

total_decay = base_decay + shelf_decay
```

And the value calculation is still: `shelf_life - age - total_decay`. Age is also more nuanced in this version of the challenge: `pickedUpAt - readyAt`.


To set a custom topology, you can provide one in the config, for example:

```yaml
kitchen:
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
        - cold
```

Additionally, other types of shelves can be implemented using the `kitchen.Shelf` interface and by modifying the `kitchen.shelfConfig` to instantiate them.
 
### API ### 

*Encoding/Transport* 

The APIs provided are all HTTP, using JSON as the encoding. In practice, I'd prefer to use an IDL (like gRPC) and a strongly typed encoding- but avoided it here for simplicity. 

*APIs*

* POST `/order`      - Create a new Order
* GET  `/order`      - Return all Orders
* POST `/order/{id}` - Update a specific Order (only state is supported)
* GET  `/order/{id}` - Fetch a specific Order


# Future Work #

Some things I'd like to explore with this service wrapper if I had more time/money:

* LB / TLS termination w/ Envoy
* Service to Service authentication via SPIFFE
* Distributed policy enforcement via OPA
