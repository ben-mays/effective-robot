package main

import (
	"fmt"
	"os"

	"github.com/ben-mays/effective-robot/kitchen"
	"github.com/ben-mays/effective-robot/server"
	"go.uber.org/config"
	"go.uber.org/fx"
)

const (
	// EnvKey is the environment variable that represents the runtime environment
	EnvKey string = "SERVICE_ENV"
)

type Env string

// getEnv attempts to read the environment. If unsuccessful to authoritatively determine
// the env, returns Development.
func getEnv() Env {
	env, exists := os.LookupEnv(EnvKey)
	if !exists || len(env) == 0 {
		return "development"
	}
	return Env(env)
}

// LoadConfig will figure out the environment and return a ready config.Provider.
// The provider is passed to subsystems that will correspond to top-level keys in the config,
// e.g.:
//
//   -- config/production.yaml
//
//     envoy:
//        service: xyz
//
//     redis:
//        host: localhost
//
//   -- envoy/envoy.go
//
//     type Envoy struct {
// 	     Config Config
//     }
//
//     type Config struct {
//       Service string `yaml:service`
//     }
//
//     New(provider config.Provider) Envoy {
//       var cfg Config
//       cfg := provider.Get("envoy").Populate(&cfg)
//	     return Envoy{Config: cfg}
//     }
//
func loadConfig(env Env) config.Provider {
	configPath := fmt.Sprintf("config/%s.yaml", env)
	return config.NewYAMLProviderFromFiles(configPath)
}

// ProvideXXX functions inject instances into the application DI container.
func ProvideEnv() Env {
	return getEnv()
}

func ProvideConfig(env Env) config.Provider {
	return loadConfig(env)
}

func main() {
	// app is the application container. Fx will wire everything up and expose fx.Lifecycle as a mechanism
	// to attach to the application lifecycle afterwards.
	app := fx.New(
		fx.NopLogger,
		fx.Provide(ProvideEnv, ProvideConfig),
		fx.Provide(kitchen.NewKitchen),
		fx.Provide(server.Provide),
		fx.Invoke(server.Start),
	)
	// Run will block until a SIGKILL or SIGTERM
	app.Run()
}
