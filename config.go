package main

import (
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/consul/api"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

func loadConfigOnly(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("could not read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse config YAML: %w", err)
	}
	return &cfg, nil
}

func watchConfig(configPath string, globalRouter *atomic.Value, consulClient *api.Client) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create file watcher: %v", err)
	}
	configDir := filepath.Dir(configPath)
	configName := filepath.Base(configPath)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) == configName {
					if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
						log.Println("Config file modified. Reloading...")
						time.Sleep(100 * time.Millisecond)
						newCfg, err := loadConfigOnly(configPath)
						if err != nil {
							log.Printf("Error reloading config: %v. Keeping old config.", err)
							continue
						}
						newRouter := buildRouter(newCfg, consulClient)
						globalRouter.Store(newRouter)
						log.Println("Hot reload complete. New configuration is active.")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("File watcher error:", err)
			}
		}
	}()

	err = watcher.Add(configDir)
	if err != nil {
		log.Fatalf("Failed to add config directory to watcher: %v", err)
	}
	log.Printf("Watching for config changes in directory: %s", configDir)
}
