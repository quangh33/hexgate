package main

import (
	"fmt"
	"github.com/hashicorp/consul/api"
	"gopkg.in/yaml.v3"
	"log"
	"sync/atomic"
)

const consulConfigKey = "hexgate/config"

func parseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse config YAML: %w", err)
	}
	return &cfg, nil
}

func loadInitialConfigFromConsul(client *api.Client, key string) (*Config, uint64, error) {
	log.Printf("Loading initial configuration from Consul KV: %s", key)
	kvPair, _, err := client.KV().Get(key, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch initial config from Consul: %w", err)
	}

	if kvPair == nil {
		return nil, 0, fmt.Errorf("config key '%s' not found in Consul", key)
	}

	cfg, err := parseConfig(kvPair.Value)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse initial config from Consul: %w", err)
	}

	log.Println("Successfully loaded initial configuration from Consul")
	return cfg, kvPair.ModifyIndex, nil
}

func watchConsulConfig(key string, initialIndex uint64, globalRouter *atomic.Value, consulClient *api.Client) {
	log.Printf("Starting Consul config watcher for key: %s", key)
	lastIndex := initialIndex

	for {
		opts := &api.QueryOptions{
			WaitIndex: lastIndex,
		}
		kvPair, meta, err := consulClient.KV().Get(key, opts)
		if err != nil {
			log.Printf("Error watching Consul config key %s: %v. Retrying in 5s...", key, err)
			continue
		}

		if kvPair == nil {
			log.Printf("Config key '%s' is missing from Consul. Keeping old config.", key)
			lastIndex = meta.LastIndex
			continue
		}

		if kvPair.ModifyIndex == lastIndex {
			continue
		}

		log.Println("Configuration change detected in Consul. Reloading...")
		lastIndex = kvPair.ModifyIndex

		newCfg, err := parseConfig(kvPair.Value)
		if err != nil {
			log.Printf("Error reloading config from Consul: %v. Keeping old config.", err)
			continue
		}

		newRouter := buildRouter(newCfg, consulClient)
		globalRouter.Store(newRouter)
		log.Println("Hot reload from Consul complete. New configuration is active.")
	}
}
