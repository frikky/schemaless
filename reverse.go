package schemaless

/*
Runs a reverse search through JSON, to find the schemaless based on two inputs
*/

import (
	"log"
	"fmt"
	"strings"
	"reflect"
	"encoding/json"
)

// Recursive function to search for the location in the map and put the value there
func MapValueToLocation(mapToSearch map[string]interface{}, location, value string) map[string]interface{} {

	// Split the location into parts
	locationParts := strings.Split(location, ".")

	// Iterate over the map and search for the location
	for key, mapValue := range mapToSearch {
		if key != locationParts[0] {
			continue
		}

		if len(locationParts) == 1 {
			// We've reached the end of the location, set the value
			mapToSearch[key] = value
		} else {
			// Continue searching
			newMap := make(map[string]interface{})
			if val, ok := mapValue.(map[string]interface{}); ok {
				for k, v := range val {
					newMap[k] = v
				}

			} else if val, ok := mapValue.([]interface{}); ok {
				// So in this case, the 'content' itself is an array.
				// This means we need to check the NEXT variable, which should be
				// #, #0, #1, #0-2 etc.
				// FIXME: check the NEXT key for LOOP variables to put it in the RIGHT index
				// if it's in #, put it in ALL
				correctIndexes := []int{}
				if len(locationParts) > 1 && strings.HasPrefix(locationParts[1], "#") {
					if locationParts[1] == "#" {
						for i, _ := range val {
							correctIndexes = append(correctIndexes, i)
						}
					} else if strings.Contains(locationParts[1], "-") {
						// Split the string into parts
						parts := strings.Split(locationParts[1], "-")
						if len(parts) != 2 {
							log.Printf("[ERROR] Schemaless: Bad loop mapping with key %#v -> %#v (1)", locationParts[0], locationParts[1])
							continue
						}

						// Get the start and end indexes
						startIndex := strings.TrimPrefix(parts[0], "#")
						endIndex := strings.TrimPrefix(parts[1], "#")
						startIndexInt := 0
						endIndexInt := 0

						_, err := fmt.Sscanf(startIndex, "%d", &startIndexInt)
						if err != nil {
							if len(startIndex) == 0 || startIndex == "min" {
								startIndexInt = 0
							} else {
								log.Printf("[ERROR] Schemaless: Bad loop mapping with key %#v -> %#v (2)", locationParts[0], locationParts[1])
								continue
							}
						}

						_, err = fmt.Sscanf(endIndex, "%d", &endIndexInt)
						if err != nil {
							if len(endIndex) == 0 || endIndex == "max" {
								endIndexInt = len(val) - 1
							} else {
								log.Printf("[ERROR] Schemaless: Bad loop mapping with key %#v -> %#v (3)", locationParts[0], locationParts[1])
								continue
							}
						}

						// Check if the start and end indexes are valid
						if startIndexInt < 0 {
							log.Printf("[ERROR] Schemaless: Bad loop mapping with key %#v -> %#v (4)", locationParts[0], locationParts[1])
							continue
						}

						// Add the indexes to the array
						for i := startIndexInt; i <= endIndexInt; i++ {
							correctIndexes = append(correctIndexes, i)
						}
					} else if strings.Contains(locationParts[0], "#") {
						// Find the number after # and use it statically
						index := strings.TrimPrefix(locationParts[1], "#")
						indexInt := 0
						_, err := fmt.Sscanf(index, "%d", &indexInt)
						if err != nil {
							if index == "min" {
								indexInt = 0
							} else if index == "max" {
								indexInt = len(val) - 1
							} else {
								log.Printf("[ERROR] Schemaless: Bad loop mapping with key %#v -> %#v (5)", locationParts[0], locationParts[1])
								continue
							}
						}

						// Check if the index is valid
						if indexInt < 0 {
							log.Printf("[ERROR] Schemaless: Bad loop mapping with key %#v -> %#v (6)", locationParts[0], locationParts[1])
							continue
						}

						// Add the index to the array
						correctIndexes = append(correctIndexes, indexInt)
					} else {
						for i, _ := range val {
							correctIndexes = append(correctIndexes, i)
						}
					}

				} else {
					// Add all indexes to the array (lol)
					for i, _ := range val {
						correctIndexes = append(correctIndexes, i)
					}
				}

				//newMap[key] = make([]interface{}, 0)
				loopMap := make([]interface{}, 0)
				for i, v := range val {
					foundIndex := false
					for _, index := range correctIndexes {
						if i == index {
							foundIndex = true
							break
						}
					}

					if !foundIndex {
						loopMap = append(loopMap, v)
						continue
					}


					if subValue, ok := v.(map[string]interface{}); ok {
						//newMap[key] = append(subValue[key].([]interface{}), mapValue)
						//_ = subValue 
						splitLocationParts := locationParts[1:]
						if len(splitLocationParts) < 2 {
							log.Printf("[ERROR] Schemaless: (1) handling []interface{} with arbitary values. This MAY not work. '%#v' -> %#v", key, mapValue)
							continue
						}

						if strings.Contains(splitLocationParts[0], "#") {
							// Remove first index
							splitLocationParts = splitLocationParts[1:]
						}

						// Recurse
						loopMap = append(loopMap, MapValueToLocation(subValue, strings.Join(splitLocationParts, "."), value))

					} else {
						//if debug { 
						//	log.Printf("[DEBUG] Schemaless: No LOOP sub-handler for replacing values of type %#v. Value: %#v", reflect.TypeOf(v).String(), v)
						//}

						loopMap = append(loopMap, v)
					}
				}

				mapToSearch[key] = loopMap 
				continue
			} else {
				log.Printf("[ERROR] Schemaless handling unknown type %#v. Value: %#v", reflect.TypeOf(mapValue).String(), value)
				continue
			}

			// Recurse and go deeper (:
			mapToSearch[key] = MapValueToLocation(newMap, strings.Join(locationParts[1:], "."), value)
		}
	}

	return mapToSearch
}


func FindMatchingString(stringToFind string, mapToSearch map[string]interface{}) string {
	for key, value := range mapToSearch {
		if _, ok := value.(string); !ok {
			continue
		} 

		foundValue := value.(string)
		if foundValue == stringToFind {
			return key
		}
	}

	return ""
}

// Recursive function to search for the schemaless in the map
func ReverseTranslate(sourceMap, searchInMap map[string]interface{}) (string, error) {
	newMap := make(map[string]string)
	for key, _ := range searchInMap {
		newMap[key] = ""
	}

	for key, value := range sourceMap {
		if val, ok := value.(string); ok {
			val = strings.TrimSpace(val)
			if strings.HasPrefix(val, "{") && strings.HasSuffix(val, "}") && strings.Contains(val, "\"") {

				mapped := make(map[string]interface{})
				err := json.Unmarshal([]byte(val), &mapped)
				if err != nil {
					log.Printf("[ERROR] Unmarshalling failed for JSON: %v", err)
				}
				//} else {
				//	log.Printf("JSON found: %#v", mapped)
				//}

				value = mapped
			}
		}

		if _, ok := value.(string); !ok {
			// Check if it's a map and try to find the value in it
			if val, ok := value.(map[string]string); ok {
				newVal := make(map[string]interface{})
				for k, v := range val {
					newVal[k] = v
				}

				value = newVal
			}

			if val, ok := value.(map[string]interface{}); ok {
				// Recursively search for the value in the map
				output, err := ReverseTranslate(val, searchInMap)
				if err != nil {
					log.Printf("[ERROR] Recursion failed on key %s: %v", key, err)
					continue
				}

				outputMap := make(map[string]string)
				err = json.Unmarshal([]byte(output), &outputMap)
				if err != nil {
					log.Printf("[ERROR] Unmarshalling failed for outputmap: %v", err)
					continue
				}

				for k, v := range outputMap {
					if v == "" {
						continue
					}

					newMap[k] = key + "." + v
				}
			} else if val, ok := value.([]interface{}); ok {
				//log.Printf("List found: %#v", val)
				// Check if it's a list and try to find the value in it
				for i, v := range val {
					if stringval, ok := v.(string); ok {
						if stringval == "" {
							continue
						}

		
						matching := FindMatchingString(stringval, searchInMap)
						if len(matching) == 0 {
							//log.Printf("No matching found for %#v (2)", stringval)
							continue
						}

						newMap[matching] = fmt.Sprintf("%s.#%d", key, i)


					} else if mapval, ok := v.(map[string]interface{}); ok {
						// Recursively search for the value in the map
						output, err := ReverseTranslate(mapval, searchInMap)
						if err != nil {
							log.Printf("[ERROR] Recursion failed on key %s: %v", key, err)
							continue
						}

						outputMap := make(map[string]string)
						err = json.Unmarshal([]byte(output), &outputMap)
						if err != nil {
							log.Printf("[ERROR] Unmarshalling failed for outputmap: %v", err)
							continue
						}

						for k, v := range outputMap {
							if v == "" {
								continue
							}

							newMap[k] = fmt.Sprintf("%s.#%d.%s", key, i, v)
						}
					} else {
						log.Printf("[ERROR] Schemaless reverse: No sublist handler for type %#v\n\nFull val: %#v", reflect.TypeOf(v).String(), val)
					}

					//newMap[matching] = key + ".#" + string(i)
				}
			} else {
				log.Printf("\n\n\n[ERROR] Schemaless reverse: No base handler for type %#v. Value: %#v\n\n\n", reflect.TypeOf(value).String(), value)
			}

			continue
		}

		// FIXME: This can crash, no? 
		// Requires weird input, but could happen
		matching := FindMatchingString(value.(string), searchInMap)
		if len(matching) == 0 {
			continue
		}

		newMap[matching] = key
	}

	reversed, err := json.MarshalIndent(newMap, "", "	")
	if err != nil {
		log.Printf("[ERROR] Marshalling failed: %v", err)
		return "", err
	}

	return string(reversed), nil
}



func removeWhitespace(input string) string {
	// Remove all whitespace from the strings
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(input, " ", ""), "\n", ""), "\t", "")
}
	
func compareOutput(reversed, expectedOutput string) bool {
	// Remove all whitespace from the strings
	reversed = removeWhitespace(reversed)
	expectedOutput = removeWhitespace(expectedOutput)
	if reversed == expectedOutput {
		return true
	}

	// Try to map to map and compare
	reversedMap := make(map[string]string)
	err := json.Unmarshal([]byte(reversed), &reversedMap)
	if err != nil {
		log.Printf("[ERROR] Unmarshalling failed for reversed: %v", err)
		return false
	}

	expectedMap := make(map[string]string)
	err = json.Unmarshal([]byte(expectedOutput), &expectedMap)
	if err != nil {
		log.Printf("[ERROR] Unmarshalling failed for expected: %v", err)
		return false
	}

	return reflect.DeepEqual(reversedMap, expectedMap)
}

func ReverseTranslateStrings(findKeys, findInData string) (string, error) {
	var sourceMap map[string]interface{}
	err := json.Unmarshal([]byte(findKeys), &sourceMap)
	if err != nil {
		log.Printf("[ERROR] Unmarshalling to map[string]interface{} failed for sourceData: %v", err)
		return "", err
	}

	var searchInMap map[string]interface{}
	err = json.Unmarshal([]byte(findInData), &searchInMap)
	if err != nil {
		log.Printf("[ERROR] Unmarshalling to map[string]interface{} failed for searchData: %v", err)
		return "", err
	}

	return ReverseTranslate(sourceMap, searchInMap)

}

func runSecondTest() {
	findKeys := `{"proj":"SHUF","title":"heyo"}`
	//findInData := `{"body":"{\n  \"fields\": {\n    \"project\": {\n      \"key\": \"SHUF\"\n    },\n    \"summary\": \"heyo\",\n    \"issuetype\": {\n      \"name\": \"Bug\"\n    }\n  }\n}\n","headers":"Content-Type=application/json\nAccept=application/json","password_basic":"","queries":"","ssl_verify":"","to_file":"","url":"https://shuffletest.atlassian.net","username_basic":""}`
	findInData := `{"body":"{\n  \"fields\": {\n    \"project\": {\n      \"key\": \"SHUF\"\n    },\n    \"summary\": \"heyo\",\n    \"issuetype\": {\n      \"name\": \"Bug\"\n    }\n  }\n}\n","headers":"Content-Type=application/json\nAccept=application/json","password_basic":"","queries":"","ssl_verify":"","to_file":"","url":"https://shuffletest.atlassian.net","username_basic":""}`

	reversed, err := ReverseTranslateStrings(findInData, findKeys)
	log.Printf("Reversed: %s", reversed)
	if err != nil {
		log.Printf("[ERROR] Reversing failed: %v", err)
	}
}

func runTest() {
	// Sample input data
	findKeys := `{
		"findme": "This is the value to find",
		"subkey": {
			"findAnother": "This is another value to find",
			"subsubkey": {
				"findAnother2": "Amazing subsubkey to find"
			},
			"sublist": [
				"This is a list",
				"This is a list",
				"Cool list item",
				"This is a list"
			],
			"objectlist": [{
				"key1": "This is a key"
			},
			{
				"key1": "Another cool thing"
			}]
		}
	}`

	// Goal is to FIND the schemaless with key "findme" in the following data
	findInData := `{
		"key1": "This is the value to find",
		"key2": "This is another value to find",
		"key3": "Amazing subsubkey to find",
		"key4": "Cool list item",
		"key5": "Another cool thing"
	}`

	// Expected output
	expectedOutput := `{
		"key1": "findme",
		"key2": "subkey.findAnother",
		"key3": "subkey.subsubkey.findAnother2",
		"key5": "subkey.objectlist.#1.key1",
		"key4": "subkey.sublist.#2"
	}`

	reversed, err := ReverseTranslateStrings(findKeys, findInData)

	/*
	var sourceMap map[string]interface{}
	err := json.Unmarshal([]byte(findKeys), &sourceMap)
	if err != nil {
		log.Printf("[ERROR] Unmarshalling failed: %v", err)
		return 
	}

	var searchInMap map[string]interface{}
	err = json.Unmarshal([]byte(findInData), &searchInMap)
	if err != nil {
		log.Printf("[ERROR] Unmarshalling failed: %v", err)
		return 
	}

	reversed, err := ReverseTranslate(sourceMap, searchInMap)
	*/

	if err != nil {
		log.Printf("[ERROR] Reversing failed: %v", err)
		return 
	}

	sameKeyValues := compareOutput(reversed, expectedOutput)
	if !sameKeyValues {
		log.Printf("Failed")
	} else {
		log.Printf("Success")
	}
}

func testMapToLocation() {
	body := `{
	  "fields": {
		"project": {
		  "key": ""
		},
		"summary": "",
		"issuetype": {
		  "name": "Bug"
		}
	  }
	}`


	mappedBody := make(map[string]interface{})
	_ = json.Unmarshal([]byte(body), &mappedBody)

	location := "fields.summary"
	value := "heyo"
	returnValue := MapValueToLocation(mappedBody, location, value)

	location = "fields.project.key"
	value = "SHUF"
	returnValue = MapValueToLocation(mappedBody, location, value)

	mappedBodyJSON, _ := json.MarshalIndent(returnValue, "", "  ")
	log.Printf("Returned value: %s", string(mappedBodyJSON))
}
