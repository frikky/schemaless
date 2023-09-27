package main

// Sample webservice to run it with

import (
	"net/http"
	"log"
	"os"
	"fmt"

	"github.com/gorilla/mux"
	"github.com/frikky/schemalessGPT"
)

func init() {
	r := mux.NewRouter()

	r.HandleFunc("/api/v1/translate/{format}", shuffle.HealthCheckHandler)

	http.Handle("/", r)
}

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "MISSING"
	}

	innerPort := os.Getenv("BACKEND_PORT")
	if innerPort == "" {
		log.Printf("[DEBUG] Running on %s:5001", hostname)
		log.Fatal(http.ListenAndServe(":5001", nil))
	} else {
		log.Printf("[DEBUG] Running on %s:%s", hostname, innerPort)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", innerPort), nil))
	}
}
