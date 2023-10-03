package main

// Sample webservice to run it with
import (
	"log"
	"os"
	"fmt"
	"net/http"
	"io/ioutil"

	"github.com/gorilla/mux"
	"github.com/frikky/schemalessGPT"
	"github.com/shuffle/shuffle-shared"
)

func TranslateWrapper(resp http.ResponseWriter, request *http.Request) {
	cors := shuffle.HandleCors(resp, request)
	if cors {
		return
	}

	body, err := ioutil.ReadAll(request.Body)
	if err != nil {
		resp.WriteHeader(401)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "%s"}`, err)))
		return
	}

	// Parse it out from the url
	format := "ticket"
	path := mux.Vars(request)
	if _, ok := path["format"]; ok {
		format = path["format"]
	} else {
		resp.WriteHeader(401)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "No format provided"}`)))
		return
	}

	log.Printf("[DEBUG] Translating to format '%s'\n\n", format)

	ctx := shuffle.GetContext(request)
	parsedOutput := schemalessGPT.Translate(ctx, format, body)

	if len(parsedOutput) == 0 {
		resp.WriteHeader(400)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "No output returned for format '%s'. Does the standard translation exist?"}`, format)))
		return
	} 

	resp.WriteHeader(200)
	resp.Write([]byte(parsedOutput))
}

func init() {
	r := mux.NewRouter()

	r.HandleFunc("/api/v1/translate_to/{format}", TranslateWrapper).Methods("OPTIONS", "POST")

	http.Handle("/", r)
}

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "MISSING"
	}

	innerPort := os.Getenv("BACKEND_PORT")
	if innerPort == "" {
		log.Printf("[DEBUG] Running on %s:5002", hostname)
		log.Fatal(http.ListenAndServe(":5002", nil))
	} else {
		log.Printf("[DEBUG] Running on %s:%s", hostname, innerPort)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", innerPort), nil))
	}
}
