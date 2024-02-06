package schemaless
//package main 

import (
	"fmt"
	"log"
	"os"
	"sort"
	"time"
	"errors"
	"context"
	"strings"
	"io/ioutil"
	"crypto/md5"
	"encoding/json"

    "gopkg.in/yaml.v3"
	//"github.com/shuffle/shuffle-shared"
	openai "github.com/sashabaranov/go-openai"

	"github.com/patrickmn/go-cache"
	"google.golang.org/appengine/memcache"
	gomemcache "github.com/bradfitz/gomemcache/memcache"
)

var mc = gomemcache.New(memcached)
var memcached = os.Getenv("SHUFFLE_MEMCACHED")
var requestCache = cache.New(60*time.Minute, 60*time.Minute)

var maxCacheSize = 1020000

func SaveQuery(inputStandard, gptTranslated string) error {
	// Write it to file in the example folder
	filename := fmt.Sprintf("queries/%s", inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		//log.Printf("[ERROR] Error opening file %s (1): %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write([]byte(gptTranslated)); err != nil {
		log.Printf("[ERROR] Schemaless: Error writing to file %s: %v", filename, err)
		return err
	}

	log.Printf("[INFO] Schemaless: Translation saved to %s", filename)
	return nil
}

func GptTranslate(keyTokenFile, standardFormat, inputDataFormat string) (string, error) {

	//systemMessage := fmt.Sprintf("Use the this standard format for the output, and neither add things at the start, nor subtract from the end. Only modify the keys. Add ONLY the most relevant matching key, and make sure each key has a value. It NEEDS to be a valid JSON message: %s", standardFormat)
	//userQuery := fmt.Sprintf(`Use the standard to translate the following input JSON data. If the key is a synonym, matching or similar between the two, add the key of the input to the value of the standard. User Input:\n%s`, inputDataFormat)

	systemMessage := fmt.Sprintf("Ensure the output is valid JSON, and does NOT add more keys to the standard. Make sure each key in the standard has a value from the user input. If values are nested, ALWAYS add the nested value in jq format such as 'secret.version.value'")
	userQuery := fmt.Sprintf(`Translate the given user input JSON structure to a standard format. Use the values from the standard to guide you what to look for. The standard format should follow the pattern:

%s

User Input:
%s

Generate the standard output structure without providing the expected output.
`, standardFormat, inputDataFormat)


	if len(os.Getenv("OPENAI_API_KEY")) == 0 {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	SaveQuery(keyTokenFile, userQuery) 

	openaiClient := openai.NewClient(os.Getenv("OPENAI_API_KEY"))
	contentOutput := ""
	cnt := 0
	for {
		if cnt >= 5 {
			log.Printf("[ERROR] Schemaless: Failed to match Formatting in standard translation after 5 tries. Returning empty string.")

			return "", errors.New("Failed to match Formatting in standard translation after 5 tries")
		}

		openaiResp2, err := openaiClient.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatCompletionMessage{
					{
						Role:    openai.ChatMessageRoleSystem,
						Content: systemMessage,
					},
					{
						Role:    openai.ChatMessageRoleUser,
						Content: userQuery,
					},
				},
				Temperature: 0,
			},
		)

		if err != nil {
			log.Printf("[ERROR] Schemaless: Failed to create chat completion in runActionAI. Retrying in 3 seconds (1): %s", err)
			time.Sleep(3 * time.Second)
			cnt += 1
			continue
		}

		contentOutput = openaiResp2.Choices[0].Message.Content
		break
	}

	return contentOutput, nil
	//return `{
	//	"message": "title",
	//	"subject": "description",
	//	"identifier": "id"
	//}`, nil
}

func GetStructureFromCache(ctx context.Context, inputKeyToken string) (map[string]interface{}, error) {
	// Making sure it's not too long
	inputKeyTokenMd5 := fmt.Sprintf("%x", md5.Sum([]byte(inputKeyToken)))

	returnStructure := map[string]interface{}{}
	returnCache, err := GetCache(ctx, inputKeyTokenMd5)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error getting cache: %v", err)
		return returnStructure, err
	}

	// Setting the structure AGAIN to make it not time out
	//cache, err := GetCache(ctx, cacheKey)
	cacheData := []byte(returnCache.([]uint8))
	err = json.Unmarshal(cacheData, &returnStructure)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Failed to unmarshal from cache: %s. Value: %s", err, cacheData)
		return returnStructure, err
	}

	// Reseting it in cache to update timing
	err = SetStructureCache(ctx, inputKeyToken, cacheData)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error setting cache for key %s: %v", inputKeyToken, err)
	}

	return returnStructure, nil 
}

func SetStructureCache(ctx context.Context, inputKeyToken string, inputStructure []byte) error {
	inputKeyTokenMd5 := fmt.Sprintf("%x", md5.Sum([]byte(inputKeyToken)))

	err := SetCache(ctx, inputKeyTokenMd5, inputStructure, 86400)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error setting cache for key %s: %v", inputKeyToken, err)
		return err
	}

	//log.Printf("[DEBUG] Schemaless: Successfully set structure for md5 '%s' in cache", inputKeyTokenMd5)

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
					newParsedValue, err := json.MarshalIndent(parsedValue, "", "\t")
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error in index %d of key %s: %v", loopItem, k, err)
						continue
					}

					returnJson, newKeyToken, err := RemoveJsonValues([]byte(string(newParsedValue)), depth+1)
					_ = newKeyToken

					if err != nil {
						log.Printf("[ERROR] Schemaless: Error: %v", err)
					} else {
						//log.Printf("returnJson (1): %v", string(returnJson))
						// Unmarshal the byte back into a map[string]interface{}
						var jsonParsed2 map[string]interface{}
						err := json.Unmarshal(returnJson, &jsonParsed2)
						if err != nil {
							log.Printf("[ERROR] Schemaless: Error: %v", err)
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
			newParsedValue, err := json.MarshalIndent(jsonParsed[k].(map[string]interface{}), "", "\t")
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in key %s: %v", k, err)
				continue
			}

			returnJson, newKeyToken, err := RemoveJsonValues([]byte(string(newParsedValue)), depth+1)

			if depth < 3 && len(newKeyToken) > 0 {
				keyToken += "." + newKeyToken
			}

			if err != nil {
				log.Printf("[ERROR] Schemaless: Error: %v", err)
			} else {
				//log.Printf("returnJson (2): %v", string(returnJson))
				// Unmarshal the byte back into a map[string]interface{}
				var jsonParsed2 map[string]interface{}
				err := json.Unmarshal(returnJson, &jsonParsed2)
				if err != nil {
					log.Printf("[ERROR] Schemaless: Error: %v", err)
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
	input, err = json.MarshalIndent(jsonParsed, "", "\t")
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

	body = YamlToJson(body)

	if b, err := json.MarshalIndent(body, "", "\t"); err != nil {
		fmt.Printf("Error: %v\n", err)
		return "", err
	} else {
		startValue = string(b)
	}

	return startValue, nil
}


func SaveTranslation(inputStandard, gptTranslated string) error {
	//if len(gptTranslated) < 3 {
	//	return nil
	//}	

	// Write it to file in the example folder
	filename := fmt.Sprintf("examples/%s.json", inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		//log.Printf("[ERROR] Error opening file %s (2): %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write([]byte(gptTranslated)); err != nil {
		log.Printf("[ERROR] Schemaless: Error writing to file %s: %v", filename, err)
		return err
	}

	log.Printf("[INFO] Schemaless: Translation saved to %s", filename)
	return nil
}

func SaveParsedInput(inputStandard string, gptTranslated []byte) error {
	// Write it to file in the example folder
	filename := fmt.Sprintf("input/%s", inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		//log.Printf("[ERROR] Schemaless: Error opening file %s (3): %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write(gptTranslated); err != nil {
		log.Printf("[ERROR] Schemaless: Error writing to file %s: %v", filename, err)
		return err
	}

	log.Printf("[INFO] Schemaless: Translation saved to %s", filename)
	return nil
}

func GetStandard(inputStandard string) ([]byte, error) {
	// Open the relevant file
	filename := fmt.Sprintf("standards/%s.json", inputStandard)
	jsonFile, err := os.Open(filename)
	if err != nil {
		//log.Printf("[ERROR] Schemaless: Error opening file %s (4): %v", filename, err)
		return []byte{}, err
	}

	// Read the file into a byte array
	byteValue, err  := ioutil.ReadAll(jsonFile)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error reading file %s: %v", filename, err)
		return []byte{}, err
	}

	return byteValue, nil
}

func GetExistingStructure(inputStandard string) ([]byte, error) {
	// Open the relevant file
	filename := fmt.Sprintf("examples/%s.json", inputStandard)
	jsonFile, err := os.Open(filename)
	if err != nil {
		//log.Printf("[ERROR] Schemaless: Error opening file %s (5): %v", filename, err)
		return []byte{}, err
	}

	// Read the file into a byte array
	byteValue, err  := ioutil.ReadAll(jsonFile)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error reading file %s: %v", filename, err)
		return []byte{}, err
	}

	return byteValue, nil
}

// Recurses to find keys deeper in the thingy
// FIXME: Does not support loops yet
// Should be able to handle jq/shuffle-json format
func recurseFindKey(input map[string]interface{}, key string, depth int) (string, error) {
	keys := strings.Split(key, ".")
	if len(keys) > 1 {
		key = keys[0]
	}

	for k, v := range input {
		if k != key {
			continue
		}

		if len(keys) == 1 {
			if val, ok := v.(string); ok {
				return val, nil
			} else if val, ok := v.(map[string]interface{}); ok {
				if b, err := json.MarshalIndent(val, "", "\t"); err != nil {
					return "", err
				} else {
					return string(b), nil
				}
			} else if val, ok := v.([]interface{}); ok { 
				if b, err := json.MarshalIndent(val, "", "\t"); err != nil {
					return "", err
				} else {
					return string(b), nil
				}
			} else if val, ok := v.(bool); ok {
				return fmt.Sprintf("%v", val), nil
			} else if val, ok := v.(float64); ok {
				return fmt.Sprintf("%v", val), nil
			} else if val, ok := v.(int); ok {
				return fmt.Sprintf("%v", val), nil
			} else {
				return "", fmt.Errorf("Value is not a string or map[string]interface{}")
			}

			return v.(string), nil
		}

		if _, ok := v.(map[string]interface{}); ok {
			foundValue, err := recurseFindKey(v.(map[string]interface{}), strings.Join(keys[1:], "."), depth + 1)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error: %v", err)
			} else {
				return foundValue, nil
			}
		}
	}

	return "", errors.New("Key not found")
}

func runJsonTranslation(ctx context.Context, inputValue []byte, translation map[string]interface{}) ([]byte, []byte, error) {
	//log.Printf("Should translate %s based on %s", string(inputValue), translation)

	// Unmarshal the byte back into a map[string]interface{}
	var parsedInput map[string]interface{}
	err := json.Unmarshal(inputValue, &parsedInput)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error in inputValue unmarshal during translation: %v", err)
		return []byte{}, []byte{}, err
	}

	// Keeping a copy of the original parsedInput which will be changed
	modifiedParsedInput := parsedInput

	// Creating a new map to store the translated values
	translatedInput := make(map[string]interface{})
	for translationKey, translationValue := range translation {

		// Find the field in the parsedInput
		found := false
		for inputKey, inputValue:= range parsedInput {
			_ = inputValue
			if inputKey != translationValue {
				continue
			}


			//log.Printf("Found field %#v in input", inputKey)
			modifiedParsedInput[translationKey] = inputValue

			// Add the translated field to the translatedInput
			translatedInput[translationKey] = inputValue
			found = true 
		}

		if !found {
			if val, ok := translationValue.(string); ok {
				if strings.Contains(val, ".") {
					//log.Printf("[DEBUG] Schemaless: Digging deeper to find field %#v in input", translationValue)

					recursed, err := recurseFindKey(parsedInput, translationValue.(string), 0)
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error in recurseFindKey for %#v: %v", translationValue, err)
					}

					modifiedParsedInput[translationKey] = recursed
					translatedInput[translationKey] = recursed
				}
			} else {
				log.Printf("[ERROR] Schemaless: Field %#v not found in input", translationValue)
			}
		}
	}

	// Marshal the map[string]interface{} back into a byte
	translatedOutput, err := json.MarshalIndent(translatedInput, "", "\t")
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error in translatedInput marshal: %v", err)
		return []byte{}, []byte{}, err
	}

	// Marshal the map[string]interface{} back into a byte
	modifiedOutput, err := json.MarshalIndent(modifiedParsedInput, "", "\t")
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error in modifiedParsedInput marshal: %v", err)
		return translatedOutput, []byte{}, err 
	}

	return translatedOutput, modifiedOutput, nil
}

func fixPaths() {
	// Check if folders "examples" and "standards" exists"
	folders := []string{"examples", "standards", "input", "queries"}
	for _, folder := range folders {
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			log.Printf("[DEBUG] Schemaless: Folder '%s' does not exist, creating it", folder)
			os.Mkdir(folder, 0755)
		}
	}

	//log.Printf("[DEBUG] Schemaless: Folders fixed. The 'Standards' folder has the standards used for translation with GPT. 'Examples' folder contains info about already translated standards.")
}


func Translate(ctx context.Context, inputStandard string, inputValue []byte) ([]byte, error) { 	
	// Check for paths
	fixPaths()

	// Doesn't handle list inputs in json
	startValue := string(inputValue)
	if !strings.HasPrefix(startValue, "{") || !strings.HasSuffix(startValue, "}") { 
		output, err := YamlConvert(startValue)
		if err != nil {
			log.Printf("[ERROR] Schemaless: %v", err)
		}

		startValue = output
	}


	returnJson, keyToken, err := RemoveJsonValues([]byte(startValue), 1)
	if err != nil {
		log.Printf("[ERROR] Schemaless: %v", err)
		return []byte{}, err
	}

	keyToken = fmt.Sprintf("%s:%s", inputStandard, keyToken)
	keyTokenFile := fmt.Sprintf("%x", md5.Sum([]byte(keyToken)))
	err = SaveParsedInput(keyTokenFile, returnJson)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error in SaveParsedInput for file %s: %v", keyToken, err)
		return []byte{}, err
	}

	// Check if the keyToken is already in cache and use that translation layer
	inputStructure, err := GetExistingStructure(keyTokenFile)
	if err != nil {
		// Check if the standard exists at all
		standardFormat, err := GetStandard(inputStandard)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in GetStandard for standard %s: %v", inputStandard, err)
			return []byte{}, err
		}

		gptTranslated, err := GptTranslate(keyTokenFile, string(standardFormat), string(returnJson))
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in GptTranslate: %v", err)
			return []byte{}, err
		}

		//log.Printf("\n\n[DEBUG] GPT translated: %v. Should save this to file in folder 'examples' with filename %s\n\n", string(gptTranslated), keyTokenFile)
		err = SaveTranslation(keyTokenFile, gptTranslated)
		if err != nil {
			log.Printf("[ERROR] Error in SaveTranslation: %v", err)
			return []byte{}, err
		}

		log.Printf("[DEBUG] Saved translation to file. Should now run OpenAI and set cache!")
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
		//log.Printf("[INFO] Structure received: %v", returnStructure)
	}

	//log.Printf("returnStructure: %#v", returnStructure)
	//log.Printf("keyToken: %v", keyToken)

	translation, modifiedInput, err := runJsonTranslation(ctx, []byte(startValue), returnStructure)
	if err != nil {
		log.Printf("[ERROR] Error in runJsonTranslation: %v", err)
		return []byte{}, err
	}  

	_ = modifiedInput
	//log.Printf("translation: %v", string(translation))
	//log.Printf("modifiedInput: %v", string(modifiedInput))

	return translation, nil
}



// Cache handlers
func DeleteCache(ctx context.Context, name string) error {
	if len(memcached) > 0 {
		return mc.Delete(name)
	}

	if false {
		return memcache.Delete(ctx, name)

	} else {
		requestCache.Delete(name)
		return nil
	}

	return errors.New(fmt.Sprintf("No cache found for %s when DELETING cache", name))
}

// Cache handlers
func GetCache(ctx context.Context, name string) (interface{}, error) {
	if len(name) == 0 {
		log.Printf("[ERROR] No name provided for cache")
		return "", nil
	}

	name = strings.Replace(name, " ", "_", -1)

	if len(memcached) > 0 {
		item, err := mc.Get(name)
		if err == gomemcache.ErrCacheMiss {
			//log.Printf("[DEBUG] Cache miss for %s: %s", name, err)
		} else if err != nil {
			//log.Printf("[DEBUG] Failed to find cache for key %s: %s", name, err)
		} else {
			//log.Printf("[INFO] Got new cache: %s", item)

			if len(item.Value) == maxCacheSize {
				totalData := item.Value
				keyCount := 1
				keyname := fmt.Sprintf("%s_%d", name, keyCount)
				for {
					if item, err := mc.Get(keyname); err != nil {
						break
					} else {
						if totalData != nil && item != nil && item.Value != nil {
							totalData = append(totalData, item.Value...)
						}

						//log.Printf("%d - %d = ", len(item.Value), maxCacheSize)
						if len(item.Value) != maxCacheSize {
							break
						}
					}

					keyCount += 1
					keyname = fmt.Sprintf("%s_%d", name, keyCount)
				}

				// Random~ high number
				if len(totalData) > 10062147 {
					//log.Printf("[WARNING] CACHE: TOTAL SIZE FOR %s: %d", name, len(totalData))
				}
				return totalData, nil
			} else {
				return item.Value, nil
			}
		}

		return "", errors.New(fmt.Sprintf("No cache found in SHUFFLE_MEMCACHED for %s", name))
	}

	if false {

		if item, err := memcache.Get(ctx, name); err != nil {

		} else if err != nil {
			return "", errors.New(fmt.Sprintf("Failed getting CLOUD cache for %s: %s", name, err))
		} else {
			// Loops if cachesize is more than max allowed in memcache (multikey)
			if len(item.Value) == maxCacheSize {
				totalData := item.Value
				keyCount := 1
				keyname := fmt.Sprintf("%s_%d", name, keyCount)
				for {
					if item, err := memcache.Get(ctx, keyname); err != nil {
						break
					} else {
						totalData = append(totalData, item.Value...)

						//log.Printf("%d - %d = ", len(item.Value), maxCacheSize)
						if len(item.Value) != maxCacheSize {
							break
						}
					}

					keyCount += 1
					keyname = fmt.Sprintf("%s_%d", name, keyCount)
				}

				// Random~ high number
				if len(totalData) > 10062147 {
					//log.Printf("[WARNING] CACHE: TOTAL SIZE FOR %s: %d", name, len(totalData))
				}
				return totalData, nil
			} else {
				return item.Value, nil
			}
		}
	} else {
		if value, found := requestCache.Get(name); found {
			return value, nil
		} else {
			return "", errors.New(fmt.Sprintf("Failed getting ONPREM cache for %s", name))
		}
	}

	return "", errors.New(fmt.Sprintf("No cache found for %s", name))
}

// Sets a key in cache. Expiration is in minutes.
func SetCache(ctx context.Context, name string, data []byte, expiration int32) error {
	// Set cache verbose
	//if strings.Contains(name, "execution") || strings.Contains(name, "action") && len(data) > 1 {
	//}

	if len(name) == 0 {
		log.Printf("[WARNING] Key '%s' is empty with value length %d and expiration %d. Skipping cache.", name, len(data), expiration)
		return nil
	}

	// Maxsize ish~
	name = strings.Replace(name, " ", "_", -1)

	// Splitting into multiple cache items
	if len(memcached) > 0 {
		comparisonNumber := 50
		if len(data) > maxCacheSize*comparisonNumber {
			return errors.New(fmt.Sprintf("Couldn't set cache for %s - too large: %d > %d", name, len(data), maxCacheSize*comparisonNumber))
		}

		loop := false
		if len(data) > maxCacheSize {
			loop = true
			//log.Printf("Should make multiple cache items for %s", name)
		}

		// Custom for larger sizes. Max is maxSize*10 when being set
		if loop {
			currentChunk := 0
			keyAmount := 0
			totalAdded := 0
			chunkSize := maxCacheSize
			nextStep := chunkSize
			keyname := name

			for {
				if len(data) < nextStep {
					nextStep = len(data)
				}

				parsedData := data[currentChunk:nextStep]
				item := &memcache.Item{
					Key:        keyname,
					Value:      parsedData,
					Expiration: time.Minute * time.Duration(expiration),
				}

				var err error
				if len(memcached) > 0 {
					newitem := &gomemcache.Item{
						Key:        keyname,
						Value:      parsedData,
						Expiration: expiration * 60,
					}

					err = mc.Set(newitem)
				} else {
					err = memcache.Set(ctx, item)
				}

				if err != nil {
					if !strings.Contains(fmt.Sprintf("%s", err), "App Engine context") {
						log.Printf("[ERROR] Failed setting cache for '%s' (1): %s", keyname, err)
					}
					break
				} else {
					totalAdded += chunkSize
					currentChunk = nextStep
					nextStep += chunkSize

					keyAmount += 1
					//log.Printf("%s: %d: %d", keyname, totalAdded, len(data))

					keyname = fmt.Sprintf("%s_%d", name, keyAmount)
					if totalAdded > len(data) {
						break
					}
				}
			}

			//log.Printf("[INFO] Set app cache with length %d and %d keys", len(data), keyAmount)
		} else {
			item := &memcache.Item{
				Key:        name,
				Value:      data,
				Expiration: time.Minute * time.Duration(expiration),
			}

			var err error
			if len(memcached) > 0 {
				newitem := &gomemcache.Item{
					Key:        name,
					Value:      data,
					Expiration: expiration * 60,
				}

				err = mc.Set(newitem)
			} else {
				err = memcache.Set(ctx, item)
			}

			if err != nil {
				if !strings.Contains(fmt.Sprintf("%s", err), "App Engine context") {
					log.Printf("[WARNING] Failed setting cache for key '%s' with data size %d (2): %s", name, len(data), err)
				} else {
					log.Printf("[ERROR] Something bad with App Engine context for memcache (key: %s): %s", name, err)
				}
			}
		}

		return nil
	} else {
		requestCache.Set(name, data, time.Minute*time.Duration(expiration))
	}

	return nil
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
//	startValue := []byte(`{"title": "Here is a message for you", "description": "What is this?", "severity": "High", "status": "Open", "time_taken": "125", "id": "1234"}`)
//
//	allStandards := []string{"ticket"}
//
//	ctx := context.Background()
//	Translate(ctx, allStandards[0], startValue)
//}
