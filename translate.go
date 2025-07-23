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
	"reflect"
	"strings"
	"io/ioutil"
	"math/rand"
	"crypto/md5"
	"encoding/json"
	"sync"

    "gopkg.in/yaml.v3"
	"encoding/base64"
	openai "github.com/sashabaranov/go-openai"
	"github.com/google/go-github/v28/github"
	"github.com/osteele/liquid"

	"context"
)

//var chosenModel = "gpt-4-turbo-preview"
var chosenModel = "o4-mini"
var maxInputSize = 4000
var debug = os.Getenv("DEBUG") == "true"

func getRootFolder() string { 
	rootFolder := "files"
	filepath := os.Getenv("FILE_LOCATION")
	if len(filepath) > 0 {
		rootFolder = filepath
	} 

	if len(rootFolder) == 0 {
		filepath = os.Getenv("SHUFFLE_FILE_LOCATION") 
		if len(filepath) > 0 {
			rootFolder = filepath
		}
	}

	if len(rootFolder) > 0 {
		if !strings.HasSuffix(rootFolder, "/") {
			rootFolder += "/"
		}

		rootFolder += "schemaless/"
	}

	return rootFolder
}


func SaveQuery(inputStandard, gptTranslated string, shuffleConfig ShuffleConfig) error {
	if len(shuffleConfig.URL) > 0 {
		//return nil
		return AddShuffleFile(inputStandard, "translation_ai_queries", []byte(gptTranslated), shuffleConfig)
	}

	// Write it to file in the example folder
	filename := fmt.Sprintf("%squeries/%s", getRootFolder(), inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		//log.Printf("[ERROR] Error opening file %s (1): %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write([]byte(gptTranslated)); err != nil {
		log.Printf("[ERROR] Schemaless: Error writing to file %s: %v", filename, err)
		return err
	}

	if debug { 
		log.Printf("[DEBUG] Schemaless: Translation saved to %s (1)", filename)
	}

	return nil
}

func GptTranslate(keyTokenFile, standardFormat, inputDataFormat string, shuffleConfig ShuffleConfig) (string, error) {


	additionalCondition := fmt.Sprintf("")

	systemMessage := fmt.Sprintf("Ensure the output is valid JSON, and does NOT add more keys to the standard. Make sure each important key from the user input is in the standard. Empty fields in the standard are ok. If values are nested, ALWAYS add the nested value in jq format such as 'secret.version.value'. %sExample: If the standard is ```{\"id\": \"The id of the ticket\", \"title\": \"The ticket title\"}```, and the user input is ```{\"key\": \"12345\", \"fields:\": {\"summary\": \"The title of the ticket\"}}```, the output should be ```{\"id\": \"key\", \"title\": \"fields.summary\"}```", additionalCondition)

	if debug { 
		log.Printf("[DEBUG] Schemaless: Running GPT (1) with system message: %s", systemMessage)
	}

	userQuery := fmt.Sprintf("Translate the given user input JSON structure to a standard format. Use the values from the standard to guide you what to look for. The standard format should follow the pattern:\n\n```json\n%s\n```\n\nUser Input:\n```json\n%s\n```\n\nGenerate the standard output structure without providing the expected output.", standardFormat, inputDataFormat)

	if len(os.Getenv("OPENAI_API_KEY")) == 0 {
		return standardFormat, errors.New("OPENAI_API_KEY not set")
	}

	if len(inputDataFormat) > maxInputSize {
		return standardFormat, errors.New(fmt.Sprintf("Input data too long. Max is %d. Current is %d", maxInputSize, len(inputDataFormat)))
	}

	// Make md5 of the query, and put it in cache to check
	ctx := context.Background()
	md5Query := fmt.Sprintf("%x", md5.Sum([]byte(shuffleConfig.OrgId+systemMessage+userQuery)))

	// 0 - 500ms delay to ensure 50+ queries don't run for the same query at the same time
	// This ensures we MAY get very few requests instead of a ton of them
	randomSleeptime := time.Duration(rand.Intn(500)) 
	time.Sleep(randomSleeptime * time.Millisecond)

	cacheKey := fmt.Sprintf("translationquery-%s", md5Query)
	cacheKeyStarted := fmt.Sprintf("translationquery-%s-started", md5Query)
	found, err := GetCache(ctx, cacheKeyStarted)
	if err == nil && found != nil {
		// ~30 seconds to finish the query should be enough
		maxIter := 6
		sleepTime := 5
		cnt := 0
		for {
			cache, err := GetCache(ctx, cacheKey)
			if err == nil {
				contentOutput := string([]byte(cache.([]uint8)))
				return contentOutput, nil
			}

			if cnt >= maxIter {
				break
			}

			cnt += 1
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}

	} else {
		SetCache(ctx, cacheKeyStarted, []byte("started"), 1)
	}

	cache, err := GetCache(ctx, cacheKey)
	if err == nil {
		contentOutput := string([]byte(cache.([]uint8)))
		return contentOutput, nil
	}

	if debug { 
		log.Printf("[DEBUG] Schemaless: Running GPT (2) with system message: %s", systemMessage)
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

	err = SetCache(ctx, cacheKey, []byte(contentOutput), 30)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error setting cache for key %s: %v", cacheKey, err)
		return contentOutput, err
	}

	return contentOutput, nil
}

func LiquidTranslate(ctx context.Context, userInput, translatedInput []byte) ([]byte, error) {
	engine := liquid.NewEngine()
	//template := `<h1>{{ page.title }}</h1>`
	template := string(translatedInput)

	// UserInput is used for variables - 
	// e.g. : {% if errorCode == null %}1{% else %}2{% endif %}
	// This means it should look for "errorCode" as a variable

	bindings := map[string]interface{}{}
	// Unmarshal the userInput into bindings
	err := json.Unmarshal(userInput, &bindings)
	if err != nil {
		log.Printf("[ERROR] Schemaless Liquid: Error unmarshalling userInput in LiquidTranslate: %v", err)
	}

	out, err := engine.ParseAndRenderString(template, bindings)
	if err != nil { 
		log.Printf("[ERROR] Schemaless Liquid: Error parsing and rendering template in LiquidTranslate: %v", err)
	}

	return []byte(out), nil 
}

func GetStructureFromCache(ctx context.Context, inputKeyToken string) (map[string]interface{}, error) {
	// Making sure it's not too long
	inputKeyTokenMd5 := fmt.Sprintf("%x", md5.Sum([]byte(inputKeyToken)))

	returnStructure := map[string]interface{}{}
	returnCache, err := GetCache(ctx, inputKeyTokenMd5)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error getting cache key %s: %v", inputKeyTokenMd5, err)
		return returnStructure, err
	}

	// Setting the structure AGAIN to make it not time out
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
		// Used to be a goroutine
		return AddShuffleFile(inputStandard, "translation_output", []byte(gptTranslated), shuffleConfig)
		return nil
	}

	// Write it to file in the example folder
	filename := fmt.Sprintf("%stranslation_output/%s.json", getRootFolder(), inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		//log.Printf("[ERROR] Error opening file %s (2): %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write([]byte(gptTranslated)); err != nil {
		log.Printf("[ERROR] Schemaless: Error writing to file %s: %v", filename, err)
		return err
	}

	if debug { 
		log.Printf("[DEBUG] Schemaless: Translation saved to %s (2)", filename)
	}

	return nil
}

func SaveParsedInput(inputStandard string, gptTranslated []byte, shuffleConfig ShuffleConfig) error {
	if len(shuffleConfig.URL) > 0 {
		// FIXME: Should we upload everything? I think not
		return nil
		return AddShuffleFile(inputStandard, "translation_input", gptTranslated, shuffleConfig)
	}

	// Write it to file in the example folder
	filename := fmt.Sprintf("%sinput/%s", getRootFolder(), inputStandard)

	// Open the file
	f, err := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		//log.Printf("[ERROR] Schemaless: Error opening file %s (3): %v", filename, err)
		return err
	}

	// Write the translated value
	if _, err := f.Write(gptTranslated); err != nil {
		log.Printf("[ERROR] Schemaless: Error writing to file %s: %v", filename, err)
		return err
	}

	/*
	if debug { 
		log.Printf("[DEBUG] Schemaless: Response keys saved to %s (3)", filename)
	}
	*/

	return nil
}

func LoadStandardFromGithub(client github.Client, owner, repo, path, filename string) ([]*github.RepositoryContent, error) {
	var err error

	ctx := context.Background()
	files := []*github.RepositoryContent{}

	cacheKey := fmt.Sprintf("github_%s_%s_%s_%s", owner, repo, path, filename)
	cache, err := GetCache(ctx, cacheKey)
	if err == nil {
		cacheData := []byte(cache.([]uint8))
		err = json.Unmarshal(cacheData, &files)
		if err == nil && len(files) > 0 {
			return files, nil
		}
	}

	if len(files) == 0 {
		_, files, _, err = client.Repositories.GetContents(ctx, owner, repo, path, nil)
		if err != nil {
			log.Printf("[WARNING] Failed getting standard list for namespace %s: %s", path, err)
			return []*github.RepositoryContent{}, err
		}
	}

	//log.Printf("\n\n[DEBUG] Got %d file(s): %s\n\n", len(files), path)
	if len(files) == 0 {
		log.Printf("[ERROR] No files found in namespace '%s' on Github - Used for integration framework", path)
		return []*github.RepositoryContent{}, nil
	}

	matchingFiles := []*github.RepositoryContent{}
	searchname := strings.ToLower(strings.ReplaceAll(filename, " ", "_"))
	for _, item := range files {
		itemName := strings.ToLower(strings.ReplaceAll(*item.Name, " ", "_"))
		if len(itemName) > 0 && strings.HasPrefix(itemName, searchname) {
			matchingFiles = append(matchingFiles, item)
		}
	}

	files = matchingFiles
	data, err := json.Marshal(files)
	if err != nil {
		log.Printf("[WARNING] Failed marshalling in get github files: %s", err)
		return files, nil
	}

	err = SetCache(ctx, cacheKey, data, 30)
	if err != nil {
		log.Printf("[WARNING] Failed setting cache for getfiles on github '%s': %s", cacheKey, err)
	}
  
	return files, nil
}

func LoadAndSaveStandard(inputStandard string) (error) {
	client := github.NewClient(nil)
	owner := "shuffle"
	repo := "standards"
	path := "translation_standards"
	if os.Getenv("GIT_DOWNLOAD_USER") != "" {
		owner = os.Getenv("GIT_DOWNLOAD_USER")
	}

	if os.Getenv("GIT_DOWNLOAD_REPO") != "" {
		repo = os.Getenv("GIT_DOWNLOAD_REPO")
	}

	foundFiles, err := LoadStandardFromGithub(*client, owner, repo, path, inputStandard)

	if debug { 
		log.Printf("[DEBUG] Found %d files in Github for standard '%s'", len(foundFiles), inputStandard)
	}

	if err != nil {
		log.Printf("[ERROR] Failed getting standard list from Github: %s", err)
		return err
	}

	ctx := context.Background()
	for _, item := range foundFiles {
		if debug { 
			log.Printf("[DEBUG] Found file from Github '%s'", *item.Name)
		}

		fileContent, _, _, err := client.Repositories.GetContents(ctx, owner, repo, *item.Path, nil)
		if err != nil {
			log.Printf("[ERROR] Failed getting file %s: %s", *item.Path, err)
			continue
		}

		// Get the bytes of the file
		decoded, err := base64.StdEncoding.DecodeString(*fileContent.Content)
		if err != nil {
			log.Printf("[ERROR] Failed decoding standard file %s: %s", *item.Path, err)
			continue
		}

		// Save the file to the local filesystem
		filename := fmt.Sprintf("%sstandards/%s", getRootFolder(), *item.Name)
		err = ioutil.WriteFile(filename, decoded, 0644)
		if err != nil {
			log.Printf("[ERROR] Failed writing standard file %s: %s", filename, err)
			continue
		}

		log.Printf("[INFO] Schemaless: Saved standard file %s to %s", *item.Name, filename)
	}

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
	filepath := fmt.Sprintf("%sstandards/%s.json", getRootFolder(), inputStandard)
	jsonFile, err := os.Open(filepath)
	if err != nil {
		log.Printf("[INFO] Schemaless: Problem finding file %s (4): %v. Loading the standard from Github and saving.", filepath, err)

		err := LoadAndSaveStandard(inputStandard)
		if err != nil {
			log.Printf("[ERROR] Failed to load standard from Github: %s", err)
			return []byte{}, err
		}

		log.Printf("[INFO] Done loading standard '%s' from Shuffle's Github standards. Path: %s", inputStandard, filepath)

		// Re-instantiate referece to the file 
		jsonFile, err = os.Open(filepath)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error re-opening file %s (5): %v", filepath, err)
			return []byte{}, err
		}
	}

	// Read the file into a byte array
	byteValue, err  := ioutil.ReadAll(jsonFile)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error reading file %s: %v", filepath, err)
		return []byte{}, err
	}

	return byteValue, nil
}

func GetExistingStructure(inputStandard string, shuffleConfig ShuffleConfig) ([]byte, error) {
	if len(shuffleConfig.URL) > 0 {
		// Get the standard from shuffle instead, as we are storing standards there in prod
		return FindShuffleFile(inputStandard, "translation_output", shuffleConfig)
	}

	// FIXME: Should we skip this? 
	// Is there any reason to load the standard from the file system?
	//return []byte{}, nil

	// Open the relevant file
	filename := fmt.Sprintf("%stranslation_output/%s.json", getRootFolder(), inputStandard)
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

// Recurses to find keys deeper based on the standard
// Should be able to handle jq/shuffle-json format (and some liquid)
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
			// Check if v is nil
			if v == nil {
				return "", nil
			} else if val, ok := v.(string); ok {
				//log.Printf("STRING RETURN (%s): %#v", k, val)
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
				return "", fmt.Errorf("Value is not a string or map[string]interface{}, but %#v", reflect.TypeOf(v))
			}

			return v.(string), nil
		}

		parsedKey := strings.Join(keys[1:], ".")
		if mapValue, ok := v.(map[string]interface{}); ok {
			foundValue, err := recurseFindKey(mapValue, parsedKey, depth + 1)

			// Some basic error handling for "weird" keys
			if err != nil {
				// Looks for quotation issues in translations
				if strings.HasPrefix(parsedKey, `"`) && strings.Count(parsedKey, `"`) > 1 {
					// If the key is wrapped in quotes, remove them
					parsedKey = strings.ReplaceAll(parsedKey, `"`, "")
					foundValue, err = recurseFindKey(mapValue, parsedKey, depth + 1)
					if err != nil {
						log.Printf("[ERROR] Schemaless reverse (6): %v", err)
					} else {
						return foundValue, nil
					}
				}

				//if debug { 
				//	log.Printf("[DEBUG] Schemaless reverse (5): %v", err)
				//}
			} else {
				return foundValue, nil
			}
		} else if listValue, ok := v.([]interface{}); ok {
			marshalledMap, err := json.MarshalIndent(listValue, "", "\t")
			if err != nil {
				log.Printf("[ERROR] Schemaless reverse (11): Error marshalling list value: %v", err)
			} 

			// Checks if we are supposed to check the list or not
			if len(parsedKey) > 0 && string(parsedKey[0]) == "#" {
				// Trim until first dot (.)
				if strings.Contains(parsedKey, ".") {
					parts := strings.Split(parsedKey, ".")
					if len(parts) > 1 {
						// If the key is something like "#.key", then we need to check the list for that key
						parsedKey = strings.Join(parts[1:], ".")
					} else {
						// If the key is something like "#0", then we need to check the list for that index
						parsedKey = parts[0]
					}
				}

				// This means we need to return MULTIPLE items
				// Not just a single one (:
				itemList := []any{}
				for _, item := range listValue {
					if itemMap, ok := item.(map[string]interface{}); ok {
						// Recurse into the item
						foundValue, err := recurseFindKey(itemMap, parsedKey, depth + 1)
						if err != nil {
							if debug { 
								//log.Printf("[ERROR] Schemaless reverse (9): %v", err)
							}
						} else {
							// FIXME: Returning one item at a time doesn't work
							// But returning the whole array means we need to re-construct the same array multiple times over :thinking: 
							itemList = append(itemList, foundValue)
							//return foundValue, nil
						}
					} else {
						log.Printf("[ERROR] Schemaless reverse (10): Item in list is not a map[string]interface{}, but %#v", reflect.TypeOf(item))
						itemList = append(itemList, item)
					}
				}

				marshalled, err := json.Marshal(itemList)
				if err != nil {
					log.Printf("[ERROR] Schemaless reverse (12): Error marshalling item list: %v", err)
					return "", err
				}

				// This is to do some custom parsing that makes
				// schemaless["value1", "value2"] into the actual parent
				// list. This is required 
				
				// If we get them recursed with .#.# 
				if len(marshalled) > 2 {
					return "schemaless_list"+string(marshalled), nil
				} else {
					return "", nil
				}
			} else {
				log.Printf("[WARNING] Schemaless reverse (8): Invalid key '%s' (%#v) found in list: %v. Should be something like '#0' or '#.key'. Not checking the list.", parsedKey, keys, string(marshalledMap))
			}

		} else {
			log.Printf("[ERROR] Schemaless reverse (7): Key '%s' found, but value is not a map[string]interface{}: %v")
		}
	}

	return "", errors.New(fmt.Sprintf("Key '%s' not found", key))
}

func setNestedMap(m map[string]interface{}, path string, value interface{}) (map[string]interface{}, bool) {
	keys := strings.Split(path, ".")
	if len(keys) == 0 {
		return m, false
	}

	// Build the nested value to merge
	found := false
	nested := value
	for i := len(keys) - 1; i >= 0; i-- {
		if keys[i] == "#" {

			//nested = []interface{}{nested}
			//nested = map[string]interface{}{
			//	keys[i]: nested,
			//}
			continue
		} 

		if val, ok := m[keys[i]].(string); ok {
			if strings.Contains(val, "schemaless_list") {
				found = true
				nested = map[string]interface{}{
					keys[i]: nested,
				}
			} else {
				log.Printf("[DEBUG] NOT schemaless_list")
			}
		} else {
			nested = map[string]interface{}{
				keys[i]: nested,
			}
		}
	}

	// Deep merge into the existing map
	return deepMerge(copyMap(m), nested.(map[string]interface{})), found
}

// Deep copy of a map to avoid mutating the original
func copyMap(m map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(m))
	for k, v := range m {
		if subMap, ok := v.(map[string]interface{}); ok {
			copy[k] = copyMap(subMap)
		} else {
			copy[k] = v
		}
	}
	return copy
}

// Deep merge: dst gets merged with src, with nested maps merged recursively
func deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		if vMap, ok := v.(map[string]interface{}); ok {
			if dv, exists := dst[k]; exists {
				if dvMap, ok := dv.(map[string]interface{}); ok {
					dst[k] = deepMerge(dvMap, vMap)
					continue
				}
			}
		}

		dst[k] = v
	}
	return dst
}


//func handleMultiListItems(translatedInput map[string]interface{}, jsonKey, translationKey string, loopedValue string) map[string]interface{} { 

// This function wouldn't be necessary if other recursion functions
// also have this capability themselves

// translatedInput = the parent object to modify. It is actually the parent of the parents' object.
// jsonKey = the key in the parent object WHICH IS A LIST
// parsedValues = the child values from where we have the list
func handleMultiListItems(translatedInput []interface{}, parentKey string, parsedValues map[string]interface{}, listDepth, childIndex int) ([]interface{}) { 
	if !strings.Contains(parentKey, ".") && len(translatedInput) == 0 {
		translatedInput = append(translatedInput, parsedValues)
	}

	// Start recursing to find the valid keys
	// Checks: 
	// list -> subsub
	// maps -> childkeys
	// strings -> build it out.
	for childKey, v := range parsedValues {
		if val, ok := v.(map[string]interface{}); ok {
			// By passing in translatedInput we allow child objects to modify the parent?
			newKey := fmt.Sprintf("%s.%s", parentKey, childKey)
			translatedInput = handleMultiListItems(translatedInput, newKey, val, listDepth, childIndex) 

		} else if val, ok := v.([]interface{}); ok {
			newKey := fmt.Sprintf("%s.%s.#", parentKey, childKey)

			// FIXME: Re-enable this for sub-lists
			for cnt, subItem := range val {
				if parsedSubitem, ok := subItem.(map[string]interface{}); ok { 
					translatedInput = handleMultiListItems(translatedInput, newKey, parsedSubitem, listDepth+1, cnt)
				} else {
					log.Printf("[ERROR] Schemaless: List item '%s' in key '%s' is not a map[string]interface{}, but %T. This is not handled yet (1).", childKey, parentKey, subItem)
				}
			}


		} else if val, ok := v.(string); ok {
			if strings.Contains(val, "schemaless_list[") {
				//if val == "schemaless_list[]" || val == "schemaless_list" {
				//}

				foundList := strings.Split(val, "schemaless_list")
				if len(foundList) >= 2 {

					// Somehow ALWAYS look for the first INNER list
					// This makes it so that extrapolation can be done well across all fields everywhere

					matchingList := strings.Join(foundList[1:], "schemaless_list")
					unmarshalledList := []string{}
					err := json.Unmarshal([]byte(matchingList), &unmarshalledList)
					if err != nil {
						log.Printf("[ERROR] Schemaless problem in string unmarshal of %s: err", matchingList, err)
					}

					oldParentKey := parentKey
					newParentKey := parentKey
					modificationList := translatedInput
					if listDepth > 0 {
						parentKeySplit := strings.Split(parentKey, ".")

						counter := 0
						newKeySplit := []string{}
						for cnt, part := range parentKeySplit {
							if part == "#" {
								counter += 1

								if cnt > 0 { 
									newKeySplit = append(newKeySplit, parentKeySplit[cnt-1])
								}
							}

							if counter-1 == listDepth {
								newKeySplit = append(newKeySplit, part) 
							}
						}

						newParentKey = strings.Join(newKeySplit, ".")
						modificationList = []interface{}{
							parsedValues,
						}

					}

					// Reference Item in the child list
					firstItem := modificationList[0]
					var found bool
					for cnt, listValue := range unmarshalledList {
						if cnt >= len(modificationList) {
							modificationList = append(modificationList, firstItem)
						}


						// Update the translatedInput with the new value
						newKey := fmt.Sprintf("%s.%s", newParentKey, childKey)
						newKey = strings.SplitN(newKey, ".", 2)[1]

						//cntItem := modificationList[cnt].(map[string]interface{})
						//if len(listValue) > 10 {  
						//	log.Printf("Setting value %#v for key '%s' in modificationList[%d]", listValue, newKey, cnt)
						//}

						log.Printf("Listvalue to put in index '%d': %#v", childIndex, listValue)
						newModList := modificationList[cnt].(map[string]interface{})

						newModList, found = setNestedMap(newModList, newKey, listValue)
						modificationList[cnt] = newModList
						if !found {
							if debug { 
								log.Printf("[ERROR] Schemaless: Could not set nested map for key '%s' with value '%s'. Count: %#v", newKey, listValue, cnt)
							}
						}
					}

					if listDepth > 0 { 
						// Updates the child & here 
						// This shit also needs recursion.. gahh
						if debug { 
							log.Printf("UPDATING CHILD LOOP INDEX %#v", childIndex)
						}


						// And it needs to automatically find the right one
						if strings.HasPrefix(oldParentKey, ".") {
							oldParentKey = strings.Trim(oldParentKey, ".")
						}

						if debug { 
							log.Printf("[DEBUG] Potential LOOP issue: KEY TO PUT IN: %#v. '%#v' -> %#v", oldParentKey, parsedValues, modificationList)
						}

						updated := false
						for inputKey, _ := range translatedInput {
							//FIXME: Need to NOT update the index unless there is a schemaless_list[] in the key in question
							newMap := translatedInput[inputKey].(map[string]interface{})

							// This part makes it show the first one ONLY for some reason
							// Without it, it shows ONLY the last one...

							// FIXME: Re-add this for outer looping. Gotta fix inner now.
							marshalledMap, err := json.Marshal(newMap)
							if err == nil {
								comparisonString := fmt.Sprintf(`"%s":"schemaless_list[`, childKey)
								if !strings.Contains(string(marshalledMap), comparisonString) {
									log.Printf("HANDLED ALREADY: %#v", string(marshalledMap))
									continue
								}
							}

							newMap, _ = setNestedMap(newMap, oldParentKey, modificationList)
							translatedInput[inputKey] = newMap
							updated = true

							//parsedValues = newMap

							break
						}

						if !updated {
							if debug { 
								log.Printf("[DEBUG] Appended new index to parent list as it wasn't found, but needed.")
							}

							translatedInput = append(translatedInput, map[string]interface{}{})
							newMap := translatedInput[len(translatedInput)-1].(map[string]interface{})
							newMap, found = setNestedMap(newMap, oldParentKey, modificationList)
							translatedInput[len(translatedInput)-1] = newMap
						}

						parsedValues = modificationList[childIndex].(map[string]interface{})

					} else {
						translatedInput = modificationList
					}

					marshalled, _ := json.MarshalIndent(translatedInput, "", "\t")
					log.Printf("MARSHALLED (%d): %s.", listDepth, string(marshalled))
					//translatedInput = modificationList

				}
			} else {
				log.Printf("[ERROR] Schemaless: String value '%s' found in key '%s', but not a schemaless_list. This is not handled yet (2).", val, childKey)
			}
		} else {
			log.Printf("OTHER: %s (UNHANDLED)", childKey)
			//setNestedMap(translatedInput[cnt].(map[string]interface{}), newKey, listValue)
		}
	}

	return translatedInput
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
			// Skipping (for now?)
			if _, ok := translationValue.(float64); ok {
				if debug { 
					log.Printf("[DEBUG] NOT handling float/integer translations and keeping value instead. Key: %s, Value: %v", translationKey, translationValue)
				}

				translatedInput[translationKey] = translationValue
			} else if val, ok := translationValue.([]interface{}); ok {

				newOutput := []interface{}{}

				// The list part here doesn't really work as this is checking the length of the list in the STANDARD - NOT in the value from the user
				// This makes it so that the append really does... nothing 
				for _, v := range val {
					if _, ok := v.(map[string]interface{}); !ok {

						if stringVal, stringValOk := v.(string); stringValOk {
							stringKey := stringVal
							if strings.Contains(stringKey, "[]") {
							stringKey = strings.ReplaceAll(stringKey, "[]", ".#")
							}

							if strings.Contains(stringKey, `"`) {
								stringKey = strings.ReplaceAll(stringKey, `"`, "")
							}

							recursed, err := recurseFindKey(parsedInput, stringKey, 0)
							if err == nil { 
								translationValue = []string{recursed}
							}  else {
								log.Printf("[ERROR] Schemaless: Error in RecurseFindKey for key string '%s': %v", translationKey, err)
								translationValue = []string{}

								//modifiedParsedInput[translationKey] = []string{}
								//translatedInput[translationKey] = []string{}
							}

							continue
						} else {
							log.Printf("[ERROR] Schemaless: Parsed input value is not a map[string]interface{} for key '%s': %v. Type: %#v", translationKey, v, reflect.TypeOf(v))
							newOutput = append(newOutput, v)
							continue
						}
					}

					v = v.(map[string]interface{})

					// If the value is a map[string]interface{}, we need to recurse it
					newValue := make(map[string]interface{})
					marshalled, err := json.Marshal(v)
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error marshalling value for key '%s': %v", translationKey, err)
						continue
					}

					err = json.Unmarshal(marshalled, &newValue)
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error unmarshalling value for key '%s': %v", translationKey, err)
						continue
					}

					// Runs an actual translation
					output, _, err := runJsonTranslation(ctx, inputValue, newValue)
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error in runJsonTranslation for key '%s': %v", translationKey, err)
						continue
					}

					// Marshal the output back to a byte
					var outputParsed map[string]interface{}
					err = json.Unmarshal(output, &outputParsed)
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error in unmarshalling output for key '%s': %v", translationKey, err)
						
						translatedInput[translationKey] = translationValue
						continue
					}

					newOutput = append(newOutput, outputParsed)

					// Hard to optimise for subkeys -> parent control tho
					if strings.Contains(string(output), "schemaless_list[") {
						//newTranslatedInput := handleMultiListItems(newOutput, translationKey, outputParsed)
						newTranslatedInput := handleMultiListItems(newOutput, "", outputParsed, 0, 0)
						translationValue = newTranslatedInput

						newOutput = []interface{}{}
						//newOutput = append(newOutput, newTranslatedInput)
						break
					}
				}

				if len(newOutput) > 0 {
					
					translatedInput[translationKey] = newOutput
				} else {
					//if debug { 
					//	log.Printf("[ERROR] Schemaless DEBUG issue: No output found for key '%s' after translation. This COULD be working as intended.", translationKey)
					//}

					translatedInput[translationKey] = translationValue
				}

			} else if val, ok := translationValue.(map[string]interface{}); ok {

				// Recurse it with the same function again
				translation, _, err := runJsonTranslation(ctx, inputValue, val)
				if err != nil {
					log.Printf("[ERROR] Schemaless: Error in runJsonTranslation for key '%s': %v", translationKey, err)
					translatedInput[translationKey] = translationValue
					continue
				}

				// Translation here is in base64, so we need to unmarshal it again
				var translationValueParsed map[string]interface{}

				err = json.Unmarshal([]byte(translation), &translationValueParsed)
				if err != nil {
					log.Printf("[ERROR] Schemaless: Error in unmarshalling translation value for key '%s': %v", translationKey, err)
					translatedInput[translationKey] = translationValue
					continue
				}

				// Check if the translationValueParsed is empty
				if len(translationValueParsed) == 0 {
					log.Printf("[WARNING] Schemaless: Translation value for key '%s' is empty after unmarshalling. Skipping it.", translationKey)
					translatedInput[translationKey] = translationValue
					continue
				}

				translatedInput[translationKey] = translationValueParsed


			} else if val, ok := translationValue.(string); ok {
				if strings.Contains(val, ".") {
					//log.Printf("[DEBUG] Schemaless: Digging deeper to find field %#v in input", translationValue)

					// Check for ends with item.value[] <- array
					// This can't be handled properly without being something like .# or .#0
					key := translationValue.(string)
					if strings.Contains(key, "[]") {
						key = strings.ReplaceAll(key, "[]", ".#")
					}

					if strings.Contains(key, `"`) {
						key = strings.ReplaceAll(key, `"`, "")
					}

					recursed, err := recurseFindKey(parsedInput, key, 0)
					if err != nil {
						if debug { 
							log.Printf("[DEBUG] Schemaless Reverse problem: Error in RecurseFindKey for %#v: %v", key, err)
						}
					}

					modifiedParsedInput[translationKey] = recursed
					translatedInput[translationKey] = recursed
				} else {
					translatedInput[translationKey] = val
				}
			} else {
				log.Printf("[ERROR] Schemaless: Field %#v not found in input", translationValue)

				translatedInput[translationKey] = translationValue
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

// Ensures relevant folders exist
func fixPaths() {
	folders := []string{"translation_output", "standards", "input", "queries"}
	for _, folder := range folders {
		folderpath := fmt.Sprintf("%s%s", getRootFolder(), folder)
		if _, err := os.Stat(folderpath); os.IsNotExist(err) {
			if debug { 
				log.Printf("[DEBUG] Schemaless: Folder '%s' does not exist, creating it", folderpath)
			}

			err = os.MkdirAll(folderpath, 0755)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error creating folder '%s': %v", folder, err)
			}
		}
	}
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
		log.Printf("[DEBUG] Schemaless: No list key found in the sub body (1 LEVEL ONLY). No parsing to be done - returning empty list")
		return []byte(`[]`), nil
	}

	log.Printf("[DEBUG] Schemaless: Found a list of length %d in the returnJson. Should translate each item to the substandard", len(listJson))

	// For each item in the list, translate it to the substandard
	// Maybe do this with recursive Translate() calls?

	skipAfterCount := 50 
	var wg sync.WaitGroup
	var mu sync.Mutex // Mutex to safely access parsedOutput slice

	parsedOutput := [][]byte{}
	for cnt, listItem := range listJson {
		// No goroutine on the first ones as we want to make sure caching is done properly
		if cnt == 0 {
			marshalledBody, err := json.Marshal(listItem)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in marshalling of list item: %v", err)
				continue
			}

			schemalessOutput, err := Translate(ctx, subStandard, marshalledBody, authConfig, "skip_substandard")
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in schemaless.Translate for sub list item: %v", err)
				continue
			}

			parsedOutput = append(parsedOutput, schemalessOutput)
			continue
		}


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

		if cnt > skipAfterCount {
			log.Printf("[WARNING] Schemaless: Breaking after %d items in the list", skipAfterCount)
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

	// FIXME: May not be important anymore 
	// This prevents recursion inside a JSON blob
	// in case file reference is bad

	// Reference key addition is a way the user can send in a key to add to the filename, as to make it unique and configurable, even with the same input/output from the actual translation
	skipSubstandard := false
	filenamePrefix := ""
	for _, input := range inputConfig { 
		if input == "skip_substandard" {
			skipSubstandard = true
			break
		}

		if strings.HasPrefix(strings.ToLower(input), "filename_prefix:") {
			input = strings.TrimPrefix(input, "filename_prefix:")
			if len(input) > 0 {
				filenamePrefix = fmt.Sprintf("%s", input)
			}
		}
	}

	if shuffleConfig.URL == "" {
		// Check for paths
		fixPaths()
	}

	// Doesn't handle list inputs in json
	startValue := strings.TrimSpace(string(inputValue))
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

	keyTokenFile := fmt.Sprintf("%s%s-%x", filenamePrefix, inputStandard, md5.Sum([]byte(keyToken)))
	err = SaveParsedInput(keyTokenFile, returnJson, shuffleConfig)
	if err != nil {
		log.Printf("[WARNING] Schemaless: Error in SaveParsedInput for file %s: '%v'", keyTokenFile, err)
		return inputValue, nil
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
			log.Printf("[WARNING] Schemaless: Problem in GetStandard for standard %#v: %v", inputStandard, err)
			return inputValue, nil
		}

		if debug {
			log.Printf("[DEBUG] Schemaless: Got standard format for standard '%s': %s", inputStandard, string(standardFormat))
		}

		//if !skipSubstandard && len(trimmedStandard) > 2 && strings.Contains(trimmedStandard, ".json") && strings.HasPrefix(trimmedStandard, "[") && strings.HasSuffix(trimmedStandard, "]") {
		trimmedStandard := strings.TrimSpace(string(standardFormat))
		if !skipSubstandard && len(trimmedStandard) > 2 && strings.HasPrefix(trimmedStandard, "[") && strings.HasSuffix(trimmedStandard, "]") {

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
		} else {
			log.Printf("[DEBUG] Schemaless: No substandard found in the standard format for '%s'. Should continue with translation", inputStandard)
		}

		gptTranslated, err := GptTranslate(keyTokenFile, string(standardFormat), string(returnJson), shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in GptTranslate: %v", err)

			if strings.Contains(fmt.Sprintf("%s", err), "OPENAI") {
				log.Printf("[DEBUG] Schemaless: Saving standard even though no OPENAI key is supplied")
				SaveTranslation(keyTokenFile, gptTranslated, shuffleConfig)
			}

			return []byte{}, err
		}

		//log.Printf("\n\n[DEBUG] GPT translated: %v. Should save this to file in folder 'examples' with filename %s\n\n", string(gptTranslated), keyTokenFile)
		err = SaveTranslation(keyTokenFile, gptTranslated, shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Problem in SaveTranslation (3): %v", err)
			return []byte{}, err
		} 

		//log.Printf("[DEBUG] Saved GPT translation to file. Should now run OpenAI and set cache!")
		inputStructure = []byte(gptTranslated)
	}

	// FIXME: Why was this cache stuff implemented? This is confusing AF
	err = SetStructureCache(ctx, keyToken, inputStructure) 
	if err != nil {
		log.Printf("[ERROR] Schemaless: problem in SetStructureCache for keyToken %#v with inputStructure %#v: %v", keyToken, inputStructure, err)
		return []byte{}, err
	}

	returnStructure, err := GetStructureFromCache(ctx, keyToken)
	if err != nil {
		log.Printf("[ERROR] Schemaless: problem in return structure for keyToken %#v. Should run OpenAI and set cache!", keyToken)
		return []byte{}, err
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

func init() { 
	if os.Getenv("DEBUG") == "true" { 
		debug = true
	}

	if len(os.Getenv("MODEL")) > 0 {
		chosenModel = os.Getenv("MODEL")

		log.Printf("[INFO] Schemaless: Using model %s", chosenModel)
	}
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
