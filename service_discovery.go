package main

import (
	"fmt"
	"github.com/hashicorp/consul/api"
	"log"
	"time"
)

func (s *ServerPool) startConsulWatcher(client *api.Client, serviceName string) {
	log.Printf("Starting Consul watcher for service: %s", serviceName)
	var lastIndex uint64 = 0

	go func() {
		for {
			opts := &api.QueryOptions{
				WaitIndex: lastIndex, // Wait for changes *after* this index
				Near:      "_agent",
			}
			// a.k.a "What's the current list of healthy {serviceName} backends?"
			services, meta, err := client.Health().Service(serviceName, "", true, opts)
			if err != nil {
				log.Printf("Error watching Consul service %s: %v. Retrying in 5s...", serviceName, err)
				time.Sleep(5 * time.Second)
				continue
			}

			lastIndex = meta.LastIndex
			log.Printf("Consul update for %s. New index: %d. Found %d instances.", serviceName, lastIndex, len(services))

			newBackendSet := make(map[string]bool)
			for _, entry := range services {
				serviceID := entry.Service.ID
				newBackendSet[serviceID] = true

				s.mu.RLock()
				_, exists := s.backends[serviceID]
				s.mu.RUnlock()

				if !exists {
					addr := entry.Service.Address
					port := entry.Service.Port
					if addr == "" {
						addr = entry.Node.Address
					}
					serviceURL := fmt.Sprintf("http://%s:%d", addr, port)

					if err := s.AddBackend(serviceID, serviceURL); err != nil {
						log.Printf("Failed to add backend %s: %v", serviceID, err)
					}
				} else {
					// It exists, make sure it's marked as alive (in case our proxy marked it down)
					s.backends[serviceID].SetAlive(true)
				}
			}

			s.mu.RLock()
			for serviceID := range s.backends {
				if !newBackendSet[serviceID] {
					go s.RemoveBackend(serviceID)
				}
			}
			s.mu.RUnlock()
		}
	}()
}
