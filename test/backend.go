package main

import (
	"flag"
	"fmt"
	"github.com/hashicorp/consul/api"
	"log"
	"net/http"
	"os"
	"strconv"
)

// How to run:
// go run test/backend.go -port 8081 -service "user-service" -advertise-addr "host.docker.internal"
// go run test/backend.go -port 8082 -service "user-service" -advertise-addr "host.docker.internal"
// go run test/backend.go -port 8083 -service "product-service" -advertise-addr "host.docker.internal"

func main() {
	port := flag.Int("port", 0, "Port to listen on (e.g., 8081)")
	serviceName := flag.String("service", "", "Service name to register in Consul (e.g., 'user-service')")
	advertiseAddr := flag.String("advertise-addr", "127.0.0.1", "Address to advertise to Consul")

	flag.Parse()

	if *port == 0 || *serviceName == "" {
		fmt.Println("-port and -service flags are required.")
		flag.Usage()
		os.Exit(1)
	}

	portStr := strconv.Itoa(*port)
	serviceID := fmt.Sprintf("%s-%s", *serviceName, portStr)

	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		log.Fatalf("Failed to create Consul client: %v", err)
	}
	registration := &api.AgentServiceRegistration{
		ID:      serviceID,
		Name:    *serviceName,
		Port:    *port,
		Address: *advertiseAddr,
		Check: &api.AgentServiceCheck{
			HTTP:     fmt.Sprintf("http://%s:%s/health", *advertiseAddr, portStr),
			Interval: "5s",
			Timeout:  "1s",
		},
	}
	if err := client.Agent().ServiceRegister(registration); err != nil {
		log.Fatalf("Failed to register service with Consul: %v", err)
	}
	log.Printf("Successfully registered service '%s' with ID '%s' in Consul", *serviceName, serviceID)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[Backend %s] Received request for %s", *port, r.URL.Path)
		message := fmt.Sprintf("Hello from Backend (Port %s)", portStr)
		fmt.Fprintln(w, message)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[Backend %s] Received request for %s", portStr, r.URL.Path)
	})

	log.Printf("Starting backend server on port %s", portStr)
	if err := http.ListenAndServe(":"+portStr, nil); err != nil {
		log.Fatalf("Failed to start backend server: %v", err)
	}
}
