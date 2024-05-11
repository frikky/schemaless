package schemaless

/*
A package for translating from a JSON input to a standard format using OpenAI's GPT-4 API.
*/

import (
	"fmt"
	"log"
	"os"
	"sort"
	"time"
	"errors"
	"strings"
	"io/ioutil"
	"crypto/md5"
	"encoding/json"
	"sync"

    "gopkg.in/yaml.v3"
	openai "github.com/sashabaranov/go-openai"

	"context"
)

var chosenModel = "gpt-4-turbo-preview"
var maxInputSize = 4000

func SaveQuery(inputStandard, gptTranslated string, shuffleConfig ShuffleConfig) error {
	if len(shuffleConfig.URL) > 0 {
		return AddShuffleFile(inputStandard, "translation_ai_queries", []byte(gptTranslated), shuffleConfig)
	}

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

func GptTranslate(keyTokenFile, standardFormat, inputDataFormat string, shuffleConfig ShuffleConfig) (string, error) {

	//systemMessage := fmt.Sprintf("Use the this standard format for the output, and neither add things at the start, nor subtract from the end. Only modify the keys. Add ONLY the most relevant matching key, and make sure each key has a value. It NEEDS to be a valid JSON message: %s", standardFormat)
	//userQuery := fmt.Sprintf(`Use the standard to translate the following input JSON data. If the key is a synonym, matching or similar between the two, add the key of the input to the value of the standard. User Input:\n%s`, inputDataFormat)

	systemMessage := fmt.Sprintf("Ensure the output is valid JSON, and does NOT add more keys to the standard. Make sure each key in the standard has a value from the user input. If values are nested, ALWAYS add the nested value in jq format such as 'secret.version.value'. Example: If the standard is ```{\"id\": \"The id of the ticket\", \"title\": \"The ticket title\"}```, and the user input is ```{\"key\": \"12345\", \"fields:\": {\"summary\": \"The title of the ticket\"}}```, the output should be ```{\"id\": \"key\", \"title\": \"fields.summary\"}```")

	log.Printf("[DEBUG] Schemaless: Running GPT with system message: %s", systemMessage)

	userQuery := fmt.Sprintf("Translate the given user input JSON structure to a standard format. Use the values from the standard to guide you what to look for. The standard format should follow the pattern:\n\n```json\n%s\n```\n\nUser Input:\n```json\n%s\n```\n\nGenerate the standard output structure without providing the expected output.", standardFormat, inputDataFormat)

	if len(os.Getenv("OPENAI_API_KEY")) == 0 {
		return standardFormat, errors.New("OPENAI_API_KEY not set")
	}

	if len(inputDataFormat) > maxInputSize {
		return standardFormat, errors.New(fmt.Sprintf("Input data too long. Max is %d. Current is %d", maxInputSize, len(inputDataFormat)))
	}

	SaveQuery(keyTokenFile, userQuery, shuffleConfig)

	openaiClient := openai.NewClient(os.Getenv("OPENAI_API_KEY"))
	contentOutput := ""
	cnt := 0
	for {
		if cnt >= 5 {
			log.Printf("[ERROR] Schemaless: Failed to match Formatting in standard translation after 5 tries. Returning empty string.")

			return standardFormat, errors.New("Failed to match Formatting in standard translation after 5 tries")
		}

		openaiResp2, err := openaiClient.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model: chosenModel,
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
	fixedCache := FixTranslationStructure(string(cacheData)) 

	err = json.Unmarshal([]byte(fixedCache), &returnStructure)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Failed to unmarshal from cache key %s: %s. Value: %s", inputKeyTokenMd5, err, cacheData)
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
		// If key ends with a number, remove it (usually only used for custom fields. FIXME: This WILL cause edge-case problems)

		keys = append(keys, k)
	}

	sort.Strings(keys)

	// Iterate over the map[string]interface{} and remove the values 
	for _, k := range keys {
		if strings.HasSuffix(k, "1") || strings.HasSuffix(k, "2") || strings.HasSuffix(k, "3") || strings.HasSuffix(k, "4") || strings.HasSuffix(k, "5") || strings.HasSuffix(k, "6") || strings.HasSuffix(k, "7") || strings.HasSuffix(k, "8") || strings.HasSuffix(k, "9") || strings.HasSuffix(k, "0") {
			continue
		}

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
						log.Printf("[ERROR] Schemaless (1): %v", err)
					} else {
						//log.Printf("returnJson (1): %v", string(returnJson))
						// Unmarshal the byte back into a map[string]interface{}
						var jsonParsed2 map[string]interface{}
						err := json.Unmarshal(returnJson, &jsonParsed2)
						if err != nil {
							log.Printf("[ERROR] Schemaless (2): %v", err)
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
				log.Printf("[ERROR] Schemaless (3): %v", err)
			} else {
				//log.Printf("returnJson (2): %v", string(returnJson))
				// Unmarshal the byte back into a map[string]interface{}
				var jsonParsed2 map[string]interface{}
				err := json.Unmarshal(returnJson, &jsonParsed2)
				if err != nil {
					log.Printf("[ERROR] Schemaless (4): %v", err)
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

	for k, _ := range jsonParsed {
		if strings.HasSuffix(k, "1") || strings.HasSuffix(k, "2") || strings.HasSuffix(k, "3") || strings.HasSuffix(k, "4") || strings.HasSuffix(k, "5") || strings.HasSuffix(k, "6") || strings.HasSuffix(k, "7") || strings.HasSuffix(k, "8") || strings.HasSuffix(k, "9") || strings.HasSuffix(k, "0") {
			// Remove the key
			delete(jsonParsed, k)
		}
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
		fmt.Printf("[ERROR} Yaml conversion problem: %v\n", err)
		return "", err
	} else {
		startValue = string(b)
	}

	return startValue, nil
}


func FixTranslationStructure(gptTranslated string) string {
	gptTranslated = strings.TrimSpace(gptTranslated)

	if strings.HasPrefix(gptTranslated, "```") {
		if strings.Contains(gptTranslated, "```json") {
			gptTranslated = strings.TrimPrefix(gptTranslated, "```json")
		} else {
			gptTranslated = strings.TrimPrefix(gptTranslated, "```")
		}

		if strings.HasSuffix(gptTranslated, "```") {
			gptTranslated = strings.TrimSuffix(gptTranslated, "```")
		}

		gptTranslated = strings.TrimSpace(gptTranslated)
	}
	
	return gptTranslated
}

func SaveTranslation(inputStandard, gptTranslated string, shuffleConfig ShuffleConfig) error {
	// Check if the data starts with ``` or ```json and ends with ``` or ```json
	gptTranslated = FixTranslationStructure(gptTranslated)

	if len(shuffleConfig.URL) > 0 {
		go AddShuffleFile(inputStandard, "translation_output", []byte(gptTranslated), shuffleConfig)
		return nil
	}

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

func SaveParsedInput(inputStandard string, gptTranslated []byte, shuffleConfig ShuffleConfig) error {
	if len(shuffleConfig.URL) > 0 {
		return AddShuffleFile(inputStandard, "translation_input", gptTranslated, shuffleConfig)
	}

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


func GetStandard(inputStandard string, shuffleConfig ShuffleConfig) ([]byte, error) {

	if len(shuffleConfig.URL) > 0 {
		// Get the standard from shuffle instead, as we are storing standards there in prod

		return FindShuffleFile(inputStandard, "translation_standards", shuffleConfig)
	}

	if strings.HasSuffix(inputStandard, ".json") {
		inputStandard = strings.TrimSuffix(inputStandard, ".json")
	}

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

func GetExistingStructure(inputStandard string, shuffleConfig ShuffleConfig) ([]byte, error) {
	if len(shuffleConfig.URL) > 0 {
		// Get the standard from shuffle instead, as we are storing standards there in prod
		return FindShuffleFile(inputStandard, "translation_output", shuffleConfig)
	}

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
				log.Printf("[ERROR] Schemaless (5): %v", err)
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

func handleSubStandard(ctx context.Context, subStandard string, returnJson string, authConfig string) ([]byte, error) {
	log.Printf("[DEBUG] Schemaless: Finding substandard for standard '%s'", subStandard)

	// 1. Check if the original returnJson is a list
	// 2. If it doesn't HAVE a list, find a list in the data
	// 3. For each item in the list, translate it to the substandard

	// List with JSON inside it
	listJson := []interface{}{}
	err := json.Unmarshal([]byte(returnJson), &listJson)
	if err != nil {
		if !strings.Contains(fmt.Sprintf("%v", err), "cannot unmarshal") { 
			log.Printf("[ERROR] Schemaless: Error in unmarshal of returnJson in sub to a direct list: %v", err)
		}

		// Map it to a map[string]interface{} instead
		var mapJson map[string]interface{}
		err = json.Unmarshal([]byte(returnJson), &mapJson)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in unmarshal of returnJson in sub to a map: %v", err)
			return []byte{}, err
		}

		for k, v := range mapJson {
			if _, ok := v.([]interface{}); ok {
				log.Printf("[DEBUG] Schemaless: Found a list in the mapJson. Should translate each item to the substandard. Key: %s", k)
				listJson = v.([]interface{})
				break
			}
		}
	}

	if len(listJson) == 0 {
		return []byte{}, errors.New("No list key found in the sub body (1 LEVEL ONLY). No parsing to be done")
	}

	log.Printf("[DEBUG] Schemaless: Found a list of length %d in the returnJson. Should translate each item to the substandard", len(listJson))

	// For each item in the list, translate it to the substandard
	// Maybe do this with recursive Translate() calls?
	var wg sync.WaitGroup
	var mu sync.Mutex // Mutex to safely access parsedOutput slice
	parsedOutput := [][]byte{}
	for cnt, listItem := range listJson {
		wg.Add(1) // Increment the wait group counter for each goroutine

		go func(cnt int, listItem interface{}) {
			defer wg.Done() // Decrement the wait group counter when the goroutine completes

			marshalledBody, err := json.Marshal(listItem)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in marshalling of list item: %v", err)
				return
			}

			// FIXME: Override the reference file after it has been successful for one?
			schemalessOutput, err := Translate(ctx, subStandard, marshalledBody, authConfig, "skip_substandard")
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in schemaless.Translate for sub list item: %v", err)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			parsedOutput = append(parsedOutput, schemalessOutput)
		}(cnt, listItem)

		if cnt > 50 {
			log.Printf("[WARNING] Schemaless: Breaking after 10 items in the list")
			break
		}
	}

	wg.Wait() // Wait for all goroutines to finish

	// Make the [][]byte into a []byte
	finalOutput := []byte("[")
	for cnt, output := range parsedOutput {
		if cnt > 0 {
			finalOutput = append(finalOutput, []byte(",")...)
		}

		finalOutput = append(finalOutput, output...)
	}

	finalOutput = append(finalOutput, []byte("]")...)
	return finalOutput, nil
}

// Add optional argument for whether to use shuffle files or not
func Translate(ctx context.Context, inputStandard string, inputValue []byte, inputConfig ...string) ([]byte, error) {

	// shuffleConfig is an overwrite we can use. Contains in first item with comma separation in order:
	// URL
	// Authorization
	// OrgId

	shuffleConfig := ShuffleConfig{}
	if len(inputConfig) > 0 {
		parsedInput := strings.Split(inputConfig[0], ",")
		for cnt, config := range parsedInput {
			if cnt == 0 {
				shuffleConfig.URL = config
			} else if cnt == 1 {
				shuffleConfig.Authorization = config
			} else if cnt == 2 {
				shuffleConfig.OrgId = config
			} else if cnt == 3 {
				shuffleConfig.ExecutionId = config
			} else {
				log.Printf("[ERROR] Schemaless: Too many arguments for shuffleConfig")
			}
		}
	}

	if shuffleConfig.URL == "" {
		// Check for paths
		fixPaths()
	}

	// Doesn't handle list inputs in json
	startValue := string(inputValue)
	if !strings.HasPrefix(startValue, "{") || !strings.HasSuffix(startValue, "}") { 
		output, err := YamlConvert(startValue)
		if err != nil {
			log.Printf("[ERROR] Schemaless bad prefix (1): %v", err)
		}

		startValue = output
	}

	returnJson, keyToken, err := RemoveJsonValues([]byte(startValue), 1)
	if err != nil {
		log.Printf("[ERROR] Schemaless json removal (2): %v", err)
		return []byte{}, err
	}

	// Used to handle recursion and weird names
	if strings.HasSuffix(inputStandard, ".json") {
		inputStandard = strings.TrimSuffix(inputStandard, ".json")
	}

	//keyToken = fmt.Sprintf("%s:%s", inputStandard, keyToken)
	keyTokenFile := fmt.Sprintf("%s-%x", inputStandard, md5.Sum([]byte(keyToken)))
	err = SaveParsedInput(keyTokenFile, returnJson, shuffleConfig)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error in SaveParsedInput for file %s: '%v'", keyTokenFile, err)
		return []byte{}, err
	}

	// FIXME: May not be important anymore 
	// This prevents recursion inside a JSON blob
	// in case file reference is bad
	skipSubstandard := false
	for _, input := range inputConfig { 
		if input == "skip_substandard" {
			skipSubstandard = true
			break
		}
	}

	// Check if the keyToken is already in cache and use that translation layer
	//log.Printf("\n\n[DEBUG] Schemaless: Getting existing structure for keyToken: '%s'\n\n", keyTokenFile)
	inputStructure, err := GetExistingStructure(keyTokenFile, shuffleConfig)
	fixedOutput := FixTranslationStructure(string(inputStructure))
	inputStructure = []byte(fixedOutput)

	if err == nil {
		//log.Printf("\n\n[DEBUG] Schemaless: Found existing structure for keyToken: '%s': %#v\n\n", keyTokenFile, string(inputStructure))
	} else {
		// Check if the standard exists at all
		standardFormat, err := GetStandard(inputStandard, shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in GetStandard for standard %#v: %v", inputStandard, err)
			return []byte{}, err
		}

		//log.Printf("\n\nFOUND STANDARD (%s): %v\n\n", inputStandard, string(standardFormat))

		trimmedStandard := strings.TrimSpace(string(standardFormat))
		if !skipSubstandard && len(trimmedStandard) > 2 && strings.Contains(trimmedStandard, ".json") && strings.HasPrefix(trimmedStandard, "[") && strings.HasSuffix(trimmedStandard, "]") {

			standardName := strings.TrimSuffix(strings.TrimPrefix(trimmedStandard, "["), "]")
			log.Printf("[DEBUG] Schemaless: Found a JSON array in the standard. Should convert it to a map[string]interface{}. Name: %s", standardName)
			_, err := GetStandard(standardName, shuffleConfig)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in GetSubStandard for standard %#v used for lists/standard references references: %v", standardName, err)
				return []byte{}, err
			}

			//log.Printf("\n\nFOUND SUBSTANDARD (%s): %v\n\n", standardName, string(subStandard))
			// FIXME: Find the list in the inputdata. Map each item to the substandard, and then return the list

			foundConfig := ""
			if len(inputConfig) > 0 {
				foundConfig = inputConfig[0]
			}

			resp, err := handleSubStandard(ctx, standardName, startValue, foundConfig)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in handleSubStandard: %v", err)
			} else {
				return resp, nil
			}

			return []byte{}, errors.New("Finding substandard and list parsing")
		}

		gptTranslated, err := GptTranslate(keyTokenFile, string(standardFormat), string(returnJson), shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in GptTranslate: %v", err)

			// FIXME: Should make the file anyway, as to make it manually editable
			if strings.Contains(fmt.Sprintf("%s", err), "OPENAI") {
				log.Printf("[DEBUG] Saving standard even though no OPENAI key is supplied")
				SaveTranslation(keyTokenFile, gptTranslated, shuffleConfig)
			}

			return []byte{}, err
		}

		//log.Printf("\n\n[DEBUG] GPT translated: %v. Should save this to file in folder 'examples' with filename %s\n\n", string(gptTranslated), keyTokenFile)
		err = SaveTranslation(keyTokenFile, gptTranslated, shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Error in SaveTranslation (3): %v", err)
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

	//log.Printf("[DEBUG] Structure received: %v", returnStructure)

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
