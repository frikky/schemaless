package schemalessGPT 
//package main 

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
    "gopkg.in/yaml.v3"

	"github.com/shuffle/shuffle-shared"
)

func GptTranslate(standardFormat, inputDataFormat string) (string, error) {
	return `{
		"message": "title",
		"subject": "description",
		"identifier": "id"
	}`, nil
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
	err = json.Unmarshal(cacheData, &returnStructure)
	if err != nil {
		log.Printf("[ERROR] Failed to unmarshal from cache: %s. Value: %s", err, cacheData)
		return returnStructure, err
	}

	// Reseting it in cache to update timing
	err = SetStructureCache(ctx, inputKeyToken, cacheData)
	if err != nil {
		log.Printf("[ERROR] Error setting cache for key %s: %v", inputKeyToken, err)
	}

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


func SaveTranslation(inputStandard, gptTranslated string) error {
	// Write it to file in the example folder
	filename := fmt.Sprintf("examples/%s.json", inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[ERROR] Error opening file %s: %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write([]byte(gptTranslated)); err != nil {
		log.Printf("[ERROR] Error writing to file %s: %v", filename, err)
		return err
	}

	log.Printf("[INFO] Translation saved to %s", filename)
	return nil
}


func GetStandard(inputStandard string) ([]byte, error) {
	// Open the relevant file
	filename := fmt.Sprintf("standards/%s.json", inputStandard)
	jsonFile, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] Error opening file %s: %v", filename, err)
		return []byte{}, err
	}

	// Read the file into a byte array
	byteValue, err  := ioutil.ReadAll(jsonFile)
	if err != nil {
		log.Printf("[ERROR] Error reading file %s: %v", filename, err)
		return []byte{}, err
	}

	return byteValue, nil
}

func GetExistingStructure(inputStandard string) ([]byte, error) {
	// Open the relevant file
	filename := fmt.Sprintf("examples/%s.json", inputStandard)
	jsonFile, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] Error opening file %s: %v", filename, err)
		return []byte{}, err
	}

	// Read the file into a byte array
	byteValue, err  := ioutil.ReadAll(jsonFile)
	if err != nil {
		log.Printf("[ERROR] Error reading file %s: %v", filename, err)
		return []byte{}, err
	}

	return byteValue, nil
}

func runJsonTranslation(ctx context.Context, inputValue []byte, translation map[string]interface{}) ([]byte, []byte, error) {
	log.Printf("Should translate %s based on %s", string(inputValue), translation)

	// Unmarshal the byte back into a map[string]interface{}
	var parsedInput map[string]interface{}
	err := json.Unmarshal(inputValue, &parsedInput)
	if err != nil {
		log.Printf("[ERROR] Error in inputValue unmarshal during translation: %v", err)
		return []byte{}, []byte{}, err
	}

	log.Printf("parsedInput: %v", parsedInput)

	// Keeping a copy of the original parsedInput which will be changed
	modifiedParsedInput := parsedInput

	// Creating a new map to store the translated values
	translatedInput := make(map[string]interface{})

	for translationKey, translationValue := range translation {
		log.Printf("Should translate field %#v from input to %#v in standard", translationValue, translationKey)

		// Find the field in the parsedInput
		for inputKey, inputValue:= range parsedInput {
			_ = inputValue
			if inputKey == translationValue {
				log.Printf("Found field %#v in input", inputKey)


				modifiedParsedInput[translationKey] = inputValue

				// Add the translated field to the translatedInput
				translatedInput[translationKey] = inputValue
			}
		}
	}

	log.Printf("modifiedParsedInput: %v", modifiedParsedInput)
	log.Printf("translatedInput: %v", translatedInput)

	// Marshal the map[string]interface{} back into a byte
	translatedOutput, err := json.Marshal(translatedInput)
	if err != nil {
		log.Printf("[ERROR] Error in translatedInput marshal: %v", err)
		return []byte{}, []byte{}, err
	}

	// Marshal the map[string]interface{} back into a byte
	modifiedOutput, err := json.Marshal(modifiedParsedInput)
	if err != nil {
		log.Printf("[ERROR] Error in modifiedParsedInput marshal: %v", err)
		return translatedOutput, []byte{}, err 
	}

	return translatedOutput, modifiedOutput, nil
}

func fixPaths() {
	// Check if folders "examples" and "standards" exists"
	folders := []string{"examples", "standards"}
	for _, folder := range folders {
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			log.Printf("[INFO] Folder %s does not exist, creating it", folder)
			os.Mkdir(folder, 0755)
		}
	}

	log.Printf("[DEBUG] Folders fixed. The 'Standards' folder has the standards used for translation with GPT. 'Examples' folder contains info about already translated standards.")
}

func Translate(ctx context.Context, inputStandard string, inputValue []byte) []byte {
	// Check for paths
	fixPaths()


	// Doesn't handle list inputs in json
	startValue := string(inputValue)
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
		return []byte{}
	}

	log.Printf("[DEBUG] Cleaned up input values: %v", string(returnJson))

	// Check if the keyToken is already in cache and use that translation layer
	keyToken = fmt.Sprintf("%s:%s", inputStandard, keyToken)
	keyTokenFile := fmt.Sprintf("%x", md5.Sum([]byte(keyToken)))

	inputStructure, err := GetExistingStructure(keyTokenFile)
	if err != nil {
		log.Printf("\n\n[ERROR] Error in getExistingStructure for standard %s: %v\n\n", inputStandard, err)

		// Check if the standard exists at all
		standardFormat, err := GetStandard(inputStandard)
		if err != nil {
			log.Printf("[ERROR] Error in GetStandard for standard %s: %v", inputStandard, err)
			return []byte{}
		}

		gptTranslated, err := GptTranslate(string(standardFormat), string(returnJson))
		if err != nil {
			log.Printf("[ERROR] Error in GptTranslate: %v", err)
			return []byte{}
		}

		log.Printf("\n\n[DEBUG] GPT translated: %v. Should save this to file in folder 'examples'\n\n", string(gptTranslated))
		err = SaveTranslation(keyTokenFile, gptTranslated)
		if err != nil {
			log.Printf("[ERROR] Error in SaveTranslation: %v", err)
			return []byte{}
		}

		log.Printf("\n\n[DEBUG] Saved translation to file. Should now run OpenAI and set cache!\n\n")
		inputStructure = []byte(gptTranslated)
	}

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

	log.Printf("returnStructure: %#v", returnStructure)
	log.Printf("keyToken: %v", keyToken)

	translation, modifiedInput, err := runJsonTranslation(ctx, []byte(startValue), returnStructure)
	if err != nil {
		log.Printf("[ERROR] Error in runJsonTranslation: %v", err)
		return []byte{}
	}  

	log.Printf("translation: %v", string(translation))
	log.Printf("modifiedInput: %v", string(modifiedInput))

	return translation
}


//func main() {
//
//	/*
//	startValue := `Services: 
//-   Orders: 
//    -   ID: $save ID1
//        SupplierOrderCode: $SupplierOrderCode
//    -   ID: $save ID2
//        SupplierOrderCode: 111111`
//	*/
//
//	//startValue := `{"title": {"key1":"value1", "key2": 2, "key3": true, "key4": null}, "key1":"value1", "key2": 2, "key3": true, "key4": null,  "key6": [{"key1":"value1", "key2": 2, "key3": true, "key4": null}, "hello", 1, true]}`
//	startValue := `{"title": "Here is a message for you", "description": "What is this?", "severity": "High", "status": "Open", "time_taken": "125", "id": "1234"}`
//
//	allStandards := []string{"ticket"}
//
//	ctx := context.Background()
//	runTests(ctx, allStandards[0], startValue)
//}
