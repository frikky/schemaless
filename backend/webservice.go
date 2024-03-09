package main

// Sample webservice to run it with
import (
	"log"
	"strings"
	"os"
	"fmt"
	"net/http"
	"io/ioutil"
	"encoding/json"

	"github.com/gorilla/mux"
	"github.com/frikky/schemaless"
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
	parsedOutput, err := schemaless.Translate(ctx, format, body)
	if err != nil {
		log.Printf("[ERROR] Failed getting output: %s", err)
		resp.WriteHeader(400)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "Translation failed. Please try again"}`)))
		return 
	}

	if len(parsedOutput) == 0 {
		resp.WriteHeader(400)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "No output returned for format '%s'. Does the standard translation exist?"}`, format)))
		return
	} 

	resp.WriteHeader(200)
	resp.Write([]byte(parsedOutput))
}

type Standard struct {
	Name 	string `json:"name"`
	Filename string `json:"filename"`
	Folder 	string `json:"folder"`
	Data []byte `json:"data"`
}

func GetFolderFiles(foldername string) []Standard {
	var fileNames []Standard

	files, err := ioutil.ReadDir(foldername)
	if err != nil {
		log.Printf("[ERROR] Failed reading folder: %s", err)
		return fileNames
	}

	for _, file := range files {
		filedata, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", foldername, file.Name()))
		if err != nil {
			log.Printf("[ERROR] Failed reading file %s: %s", filedata, err)
			continue
		}

		parsedName := file.Name()
		if strings.Contains(parsedName, ".json") {
			parsedName = strings.Replace(parsedName, ".json", "", -1)
		}

		fileNames = append(fileNames, Standard{
			Name: parsedName,
			Data: filedata,
			Filename: file.Name(),
			Folder: foldername,
		})
	}

	return fileNames
}

func GetStandards(resp http.ResponseWriter, request *http.Request) {
	cors := shuffle.HandleCors(resp, request)
	if cors {
		return
	}

	folderPaths := GetFolderFiles("standards")
	if len(folderPaths) == 0 {
		resp.WriteHeader(400)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "No standards found"}`)))
		return
	}

	jsonOutput, err := json.Marshal(folderPaths)
	if err != nil {
		resp.WriteHeader(400)
		resp.Write([]byte(fmt.Sprintf(`{"success": false, "reason": "Failed marshalling standards"}`)))
		return
	}

	resp.WriteHeader(200)
	resp.Write([]byte(jsonOutput))
}

func init() {
	r := mux.NewRouter()

	r.HandleFunc("/api/v1/translate/to/{format}", TranslateWrapper).Methods("OPTIONS", "POST")
	r.HandleFunc("/api/v1/standards", GetStandards).Methods("OPTIONS", "GET")

	http.Handle("/", r)
}

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "MISSING"
	}

	log.Printf(`
USAGE:
$ curl localhost:5004/api/v1/translate_to/email -X POST -H "Content-Type: application/json" -d '{"title": "Here is a message for you", "description": "What is this?", "severity": "High", "status": "Open", "time_taken": "125", "id": "1234"}'
	`)

	innerPort := os.Getenv("BACKEND_PORT")
	if innerPort == "" {
		log.Printf("[DEBUG] Running on %s:5004", hostname)
		log.Fatal(http.ListenAndServe(":5004", nil))
	} else {
		log.Printf("[DEBUG] Running on %s:%s", hostname, innerPort)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", innerPort), nil))
	}
}
