package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := flag.String("port", "", "Port to listen on (e.g., 8081)")

	flag.Parse()

	if *port == "" {
		fmt.Println("-port are required.")
		flag.Usage()
		os.Exit(1)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[Backend %s] Received request for %s", *port, r.URL.Path)
		message := fmt.Sprintf("Hello from Backend (Port %s)", *port)
		fmt.Fprintln(w, message)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[Backend %s] Received request for %s", *port, r.URL.Path)
	})

	log.Printf("Starting backend server on port %s", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatalf("Failed to start backend server: %v", err)
	}
}
