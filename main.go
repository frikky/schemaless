package main

import (
	"fmt"
	"log"
	"io/ioutil"
	"os"
	"context"
	"encoding/json"
	"strings"
	"crypto/md5"
	"sort"
	//"strings"
	// import yaml
    "gopkg.in/yaml.v3"

	"github.com/shuffle/shuffle-shared"

	// Should be removed as they're unecessary. Need to change shuffle shared to not require them
)

func GptTranslate() {

}

func GetStructureFromCache(ctx context.Context, inputKeyToken string) (map[string]interface{}, error) {
	// Making sure it's not too long
	inputKeyTokenMd5 := fmt.Sprintf("%x", md5.Sum([]byte(inputKeyToken)))

	returnStructure := map[string]interface{}{}
	returnCache, err := shuffle.GetCache(ctx, inputKeyTokenMd5)
	if err != nil {
		log.Printf("[ERROR] Error getting cache: %v", err)
		return returnStructure, err
	}

	// Setting the structure AGAIN to make it not time out
	//cache, err := GetCache(ctx, cacheKey)
	cacheData := []byte(returnCache.([]uint8))
	//log.Printf("CACHEDATA: %s", cacheData)
	err = json.Unmarshal(cacheData, &returnStructure)
	if err != nil {
		log.Printf("[ERROR] Failed to ")
		return returnStructure, err
	}

	// Make returnCache into []byte
	SetStructureCache(ctx, inputKeyToken, returnCacheByte)

	return returnStructure, nil 
}

func SetStructureCache(ctx context.Context, inputKeyToken string, inputStructure []byte) error {
	inputKeyTokenMd5 := fmt.Sprintf("%x", md5.Sum([]byte(inputKeyToken)))

	err := shuffle.SetCache(ctx, inputKeyTokenMd5, inputStructure, 86400)
	if err != nil {
		log.Printf("[ERROR] Error setting cache for key %s: %v", inputKeyToken, err)
		return err
	}

	log.Printf("[DEBUG] Successfully set structure for md5 '%s' in cache", inputKeyTokenMd5)

	return nil
}

//https://stackoverflow.com/questions/40737122/convert-yaml-to-json-without-struct
func YamlToJson(i interface{}) interface{} {
    switch x := i.(type) {
    case map[interface{}]interface{}:
        m2 := map[string]interface{}{}
        for k, v := range x {
            m2[k.(string)] = YamlToJson(v)
        }
        return m2
    case []interface{}:
        for i, v := range x {
            x[i] = YamlToJson(v)
        }
    }
    return i
}


func RemoveJsonValues(input []byte, depth int64) ([]byte, string, error) {
	// Make the byte into a map[string]interface{} so we can iterate over it
	keyToken := ""

	var jsonParsed map[string]interface{}
	err := json.Unmarshal(input, &jsonParsed)
	if err != nil {
		return input, keyToken, err
	}

	// Sort the keys so we can iterate over them in order
	keys := make([]string, 0, len(jsonParsed))
	for k := range jsonParsed {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	// Iterate over the map[string]interface{} and remove the values 
	for _, k := range keys {
		keyToken += k
		// Get the value of the key as a map[string]interface{}
		//log.Printf("k: %v, %#v", k, jsonParsed[k])
		// Check if it's a list or not
		if _, ok := jsonParsed[k].([]interface{}); ok {
			// Recurse this function

			newListItem := []interface{}{}
			for loopItem, v := range jsonParsed[k].([]interface{}) {
				_ = loopItem

				if parsedValue, ok := v.(map[string]interface{}); ok {
					// Marshal the value
					newParsedValue, err := json.Marshal(parsedValue)
					if err != nil {
						log.Printf("[ERROR] Error in index %d of key %s: %v", loopItem, k, err)
						continue
					}

					returnJson, newKeyToken, err := RemoveJsonValues([]byte(string(newParsedValue)), depth+1)
					_ = newKeyToken

					if err != nil {
						log.Printf("[ERROR] Error: %v", err)
					} else {
						//log.Printf("returnJson (1): %v", string(returnJson))
						// Unmarshal the byte back into a map[string]interface{}
						var jsonParsed2 map[string]interface{}
						err := json.Unmarshal(returnJson, &jsonParsed2)
						if err != nil {
							log.Printf("[ERROR] Error: %v", err)
						} else {
							newListItem = append(newListItem, jsonParsed2)
						}
					}
				} else if _, ok := v.([]interface{}); ok {
					// FIXME: No loop in loop for now
				} else if _, ok := v.(string); ok {
					newListItem = append(newListItem, "")
				} else if _, ok := v.(float64); ok {
					newListItem = append(newListItem, 0)
				} else if _, ok := v.(bool); ok {
					newListItem = append(newListItem, false)
				} else {
					//log.Printf("[ERROR] No Handler Error in index %d of key %s: %v", loopItem, k, err)
				}
			}

			jsonParsed[k] = newListItem
		}

		// Check if it's a string
		if _, ok := jsonParsed[k].(string); ok {
			// Remove the value
			jsonParsed[k] = ""
		} else if _, ok := jsonParsed[k].(float64); ok {
			jsonParsed[k] = 0
		} else if _, ok := jsonParsed[k].(bool); ok {
			jsonParsed[k] = false
		} else if _, ok := jsonParsed[k].(map[string]interface{}); ok {
			newParsedValue, err := json.Marshal(jsonParsed[k].(map[string]interface{}))
			if err != nil {
				log.Printf("[ERROR] Error in key %s: %v", k, err)
				continue
			}

			returnJson, newKeyToken, err := RemoveJsonValues([]byte(string(newParsedValue)), depth+1)

			if depth < 3 && len(newKeyToken) > 0 {
				keyToken += "." + newKeyToken
			}

			if err != nil {
				log.Printf("[ERROR] Error: %v", err)
			} else {
				//log.Printf("returnJson (2): %v", string(returnJson))
				// Unmarshal the byte back into a map[string]interface{}
				var jsonParsed2 map[string]interface{}
				err := json.Unmarshal(returnJson, &jsonParsed2)
				if err != nil {
					log.Printf("[ERROR] Error: %v", err)
				} else {
					jsonParsed[k] = jsonParsed2
				}
			}

		} else {
			//log.Printf("[ERROR] No Handler Error in key %s: %v", k, err)
		}

		// Check if the value is a map[string]interface{}
		//if _, ok := v.(map[string]interface{}); ok {
		//	// Remove the value
		//	v = nil
		//}
	}

	// Marshal the map[string]interface{} back into a byte
	input, err = json.Marshal(jsonParsed)
	if err != nil {
		return input, keyToken, err
	}

	return input, keyToken, nil
}

func YamlConvert(startValue string) (string, error) {
	var body interface{}
	if err := yaml.Unmarshal([]byte(startValue), &body); err != nil {
		//panic(err)
		return "", err
	}

	log.Printf("\n\nYAML INPUT!\n\n")
	body = YamlToJson(body)
	log.Printf("startValue: %v", startValue)

	if b, err := json.Marshal(body); err != nil {
		fmt.Printf("Error: %v\n", err)
		return "", err
	} else {
		startValue = string(b)
	}

	return startValue, nil
}

func getTestStructure() []byte {
	// Open the relevant file
	filename := "example/base_event.json"
	jsonFile, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] Error opening file %s: %v", filename, err)
		return []byte{}
	}

	// Read the file into a byte array
	byteValue, err  := ioutil.ReadAll(jsonFile)
	if err != nil {
		log.Printf("[ERROR] Error reading file %s: %v", filename, err)
		return []byte{}
	}

	return byteValue
}

func runTests(ctx context.Context, inputStandard string, startValue string) {

	// Doesn't handle list inputs in json
	if !strings.HasPrefix(startValue, "{") || !strings.HasSuffix(startValue, "}") { 
		output, err := YamlConvert(startValue)
		if err != nil {
			log.Printf("[ERROR] Error: %v", err)
		}

		startValue = output
	}


	returnJson, keyToken, err := RemoveJsonValues([]byte(startValue), 1)
	if err != nil {
		log.Printf("[ERROR] Error: %v", err)
		return
	}

	// Check if the keyToken is already in cache and use that translation layer
	keyToken = fmt.Sprintf("%s:%s", inputStandard, keyToken)


	inputStructure := getTestStructure()
	err = SetStructureCache(ctx, keyToken, inputStructure) 
	if err != nil {
		log.Printf("[ERROR] Error in SetStructureCache: %v", err)
	}

	returnStructure, err := GetStructureFromCache(ctx, keyToken)
	if err != nil {
		log.Printf("[WARNING] Error in return structure. Should run OpenAI and set cache!")
	} else {
		log.Printf("[INFO] Structure received: %v", returnStructure)
	}

	log.Printf("returnJson: %v", string(returnJson))
	log.Printf("keyToken: %v", keyToken)
}

func main() {

	/*
	startValue := `Services: 
-   Orders: 
    -   ID: $save ID1
        SupplierOrderCode: $SupplierOrderCode
    -   ID: $save ID2
        SupplierOrderCode: 111111`
	*/

	//startValue := `{"title": {"key1":"value1", "key2": 2, "key3": true, "key4": null}, "key1":"value1", "key2": 2, "key3": true, "key4": null,  "key6": [{"key1":"value1", "key2": 2, "key3": true, "key4": null}, "hello", 1, true]}`
	startValue := `{"title": "Here is a message for you", "description": "What is this?", "severity": "High", "status": "Open", "time_taken": "125"}`

	allStandards := []string{"ticket"}

	ctx := context.Background()
	runTests(ctx, allStandards[0], startValue)


}
