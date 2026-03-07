package schemaless 

/*
A package for translating from a JSON input to a standard format using OpenAI's GPT-4 API.
*/

import (
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"regexp"

	"encoding/base64"
	"gopkg.in/yaml.v3"
	"github.com/osteele/liquid"
	"github.com/google/go-github/v28/github"
	openai "github.com/sashabaranov/go-openai"

	"context"
)

// var chosenModel = "gpt-4-turbo-preview"
var chosenModel = "gpt-5-mini"
var maxInputSize = 5000
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

	systemMessage := fmt.Sprintf(`INTRODUCTION 

Translate the given user input JSON structure to the provided standard format in the jq format. Use the values from the standard to guide you what to look for. 

END INTRODUCTION
----------
JSON PARSING RULES 

Ensure the output is valid JSON, and does NOT add more keys to the standard. Make sure each important key from the user input is in the standard. Empty fields in the standard are ok. If values are nested, ALWAYS add the nested value in jq format such as 'secret.version.value'. %sExample: If the standard is '{"id": "The id of the ticket", "title": "The ticket title"}', and the user input is '{"key": "12345", "fields:": {"id": "1234", "summary": "The title of the ticket"}}', the output should be '{"id": "key", "title": "fields.summary"}'. ALWAYS go deeper than the top level of the User Input and choose accurate values like "fields.id" instead of just "fields" where it fits.

END PARSING RULES
----------
FORMATTING RULES 

- Keep the same structure as the standard format. Do not remove fields.
- If it makes sense, you can add multiple variables in the middle of descriptive text such as 'The ticket $data.id with title $data.title has been created'
- If it is a value OR tells you exactly what the value is, just keep the value. No dollarsign or wrapping.
- Add a dollar sign in front of every translation: $key.subkey.subsubkey. 
- If the type is Integer or Number, make it an actual number - NOT a string with a number in it.
- If the type is an Array, make it an actual JSON array with all the relevant keys. Example: Array type 'firstname & lastname' becomes [{"firstname": "$data[].firstname", "lastname": "$data[].lastname"}]
- NEVER use large properties or data directly, even to map custom fields or custom attributes. E.g. $data or $data.fields is not ok. Always go as deep as possible to the specific value, such as $data.fields.id or $data.fields.customfield[1].name.
- Replace SPACE in JSON keys with underscore
- MUST output valid JSON, no matter the data type you expect!

END FORMATTING RULES
`, additionalCondition)
	// If translation is needed, you may use Liquid.

	//if debug {
	//	log.Printf("[DEBUG] Schemaless: Running GPT (1) with system message: %s", systemMessage)
	//}

	//userQuery := fmt.Sprintf("Translate the given user input JSON structure to a standard format. Use the values from the standard to guide you what to look for. The standard format should follow the pattern:\n\n```json\n%s\n```\n\nUser Input:\n```json\n%s\n```\n\nGenerate the standard output structure without providing the expected output.", standardFormat, inputDataFormat)
	userQuery := fmt.Sprintf("Standard:\n```json\n%s\n```\n\n\n\nUser Input:\n```json\n%s\n```", standardFormat, inputDataFormat)

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

	apiKey := os.Getenv("AI_API_KEY")
	aiRequestUrl := os.Getenv("AI_API_URL")
	if len(apiKey) == 0 {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	if len(aiRequestUrl) == 0 {
		aiRequestUrl = os.Getenv("OPENAI_API_URL")
	}

	if len(apiKey) == 0 {
		return standardFormat, errors.New("AI_API_KEY not set")
	}

	config := openai.DefaultConfig(apiKey)
	if len(aiRequestUrl) > 0 {
		config.BaseURL = aiRequestUrl

		if strings.Contains("azure", aiRequestUrl) {
			config.APIType = openai.APITypeAzure
		} else if strings.Contains("anthropic", aiRequestUrl) {
			config.APIType = openai.APITypeAnthropic
		} else if strings.Contains("cloudflare", aiRequestUrl) {
			config.APIType = openai.APITypeCloudflareAzure
		} else if strings.Contains("azuread", aiRequestUrl) {
			config.APIType = openai.APITypeAzureAD
		} else {
			config.APIType = openai.APITypeOpenAI
		}
	}

	openaiClient := openai.NewClientWithConfig(config)

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
				Temperature:     0,
				ReasoningEffort: "low",
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

type Valuereplace struct {
	Key   string `json:"key" datastore:"key" yaml:"key"`
	Value string `json:"value" datastore:"value,noindex" yaml:"value"`

	// Used for e.g. user input storage
	Answer string `json:"answer,omitempty" datastore:"answer,noindex" yaml:"answer,omitempty"`
}

// Fixes potential decision return or reference problems:
// {{list_tickets}} -> $list_tickets
// {{list_tickets[0].description}} -> $list_tickets.#0.description
// {{ticket.description}} -> $ticket.description
func TranslateBadFieldFormats(fields []Valuereplace, skipLiquid ...bool) []Valuereplace {
	if debug { 
		//log.Printf("[DEBUG] LOOKING FOR TRANSLATION IN FIELDS: %#v", fields)
	}

	skipLiquidCheck := false
	if len(skipLiquid) > 0 && skipLiquid[0] {
		skipLiquidCheck = true
	}

	regexMatch := `\$?\s*([a-zA-Z0-9_\.()]+)(\[[0-9]*\])?(\.[a-zA-Z0-9_()]+)(\[[0-9]*\])?\s*`
	for fieldIndex, _ := range fields {
		field := fields[fieldIndex]

		// Injecting it. Weird clause, but helps with parsing lists and the like.
		if strings.HasPrefix(field.Value, "$.") {
			field.Value = strings.ReplaceAll(field.Value, "$.", "$")
		}

		oldValue := field.Value
		//if strings.HasPrefix(field.Value, "$") && !strings.HasPrefix(field.Value, "{{") && !strings.HasSuffix(field.Value, "}}") {
		//	field.Value = fmt.Sprintf("{{%s}}", field.Value)
		//}

		// Used for parsing bad outputs 
		re := regexp.MustCompile(fmt.Sprintf(`{{%s}}`, regexMatch))
		if skipLiquidCheck {
			re = regexp.MustCompile(regexMatch)
		}

		matches := re.FindAllStringSubmatch(field.Value, -1)
		if len(matches) == 0 {
			field.Value = oldValue

			if strings.HasPrefix(field.Value, "$.") {
				field.Value = strings.ReplaceAll(field.Value, "$.", "$")
			} else {
				// Look for matches for the format $.string
				matchPattern := `([$]{1}[.]{1}([a-zA-Z0-9_@-]+\.?))`
				re := regexp.MustCompile(matchPattern)
				matches = re.FindAllStringSubmatch(field.Value, -1)

				if len(matches) > 0 {
					field.Value = strings.ReplaceAll(field.Value, "$.", "$")
				}
			}
				
			fields[fieldIndex].Value = field.Value

			// Replace it 
			continue
		}

		if !skipLiquidCheck && (!strings.Contains(field.Value, "{{") || !strings.Contains(field.Value, "}}")) {
			continue
		}

		stringBuild := "$"
		for _, match := range matches {
			for i, matchValue := range match {
				if i == 0 {
					continue
				}

				if i != 1 {
					if len(matchValue) > 0 && !strings.HasPrefix(matchValue, ".") {
						stringBuild += "."
					}
				}

				if strings.HasPrefix(matchValue, "[") && strings.HasSuffix(matchValue, "]") {
					// Find the formats:
					// [] -> #
					// [:] -> #
					// [0] -> #0
					// [0:1] -> #0-1
					// [0:] -> #0-max
					if matchValue == "[]" || matchValue == "[:]" {
						stringBuild += "#"
					} else if strings.Contains(matchValue, ":") {
						parts := strings.Split(matchValue, ":")
						if len(parts) == 2 {
							stringBuild += fmt.Sprintf("#%s-%s", parts[0], parts[1])
						} else {
							stringBuild += fmt.Sprintf("#%s-max", parts[0])
						}

						stringBuild += fmt.Sprintf("#%s", matchValue)
					} else {
						// Remove the brackets
						matchValue = strings.ReplaceAll(matchValue, "[", "")
						matchValue = strings.ReplaceAll(matchValue, "]", "")
						stringBuild += fmt.Sprintf("#%s", matchValue)
					}

					continue
				}

				stringBuild += matchValue
			}

			if len(match) > 1 {
				field.Value = strings.ReplaceAll(field.Value, match[0], stringBuild)
				fields[fieldIndex].Value = field.Value
			}

			stringBuild = "$"
		}
	}

	return fields
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
		log.Printf("[ERROR] Schemaless: Failed to unmarshal from cache key %s: %s. Value: %s\nContinuing anyway.", inputKeyTokenMd5, err, string(fixedCache))
		return returnStructure, nil 
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

// https://stackoverflow.com/questions/40737122/convert-yaml-to-json-without-struct
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
	// Due to {} or similar. Don't want to save empty standards.
	if len(inputStandard) <= 4 {
		return nil
	}

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

func LoadAndSaveStandard(inputStandard string) error {
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
		if debug { 
			log.Printf("[DEBUG] Schemaless: Problem finding file %s (4): %v. Loading the standard from Github and saving.", filepath, err)
		}

		err := LoadAndSaveStandard(inputStandard)
		if err != nil {
			log.Printf("[ERROR] Failed to load standard from Github: %s", err)
			return []byte{}, err
		}

		// Re-instantiate referece to the file
		jsonFile, err = os.Open(filepath)
		if err != nil {
			log.Printf("[ERROR] Schemaless: No standard for file %s (5): %v", filepath, err)
			return []byte{}, err
		}

		log.Printf("[INFO] Done loading standard '%s' from Shuffle's Github standards. Path: %s", inputStandard, filepath)
	}

	// Read the file into a byte array
	byteValue, err := ioutil.ReadAll(jsonFile)
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
	byteValue, err := ioutil.ReadAll(jsonFile)
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

	//if len(keys) > 0 {
	//	keys = keys[1:]
	//}

	//log.Printf("[DEBUG] depth %d: key '%s', input: %#v", depth, key, input)
	/*
		if depth == 0 {
			fmt.Println("")
			fmt.Println("")
		}
		log.Printf("[DEBUG] depth %d: keys '%#v'", depth, keys)
	*/

	for k, v := range input {
		if k != key {
			continue
		}

		if len(keys) <= 1 {

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
			} else if val, ok := v.(int64); ok {
				return fmt.Sprintf("%v", val), nil
			} else {
				return "", fmt.Errorf("Value is not a string or map[string]interface{}, but %#v", reflect.TypeOf(v))
			}
		}

		parsedKey := strings.Join(keys[1:], ".")
		if mapValue, ok := v.(map[string]interface{}); ok {
			foundValue, err := recurseFindKey(mapValue, parsedKey, depth+1)

			// Some basic error handling for "weird" keys
			if err != nil {
				// Looks for quotation issues in translations
				if strings.HasPrefix(parsedKey, `"`) && strings.Count(parsedKey, `"`) > 1 {
					// If the key is wrapped in quotes, remove them
					parsedKey = strings.ReplaceAll(parsedKey, `"`, "")
					foundValue, err = recurseFindKey(mapValue, parsedKey, depth+1)
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
						foundValue, err := recurseFindKey(itemMap, parsedKey, depth+1)
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
					toReturn := fmt.Sprintf("schemaless_list%s", string(marshalled))
					return toReturn, nil
				} else {
					return "", nil
				}
			} else {
				log.Printf("[WARNING] Schemaless reverse (8): Invalid key '%s' (%#v) found in list: %v. Should be something like '#0' or '#.key'. Not checking the list.", parsedKey, keys, string(marshalledMap))
			}

			// FIXME: Do these work properly?
			//} else if stringValue, ok := v.(string); ok {
			//	log.Printf("[DEBUG] Schemaless reverse (12): Key '%s' found, returning string value: %s", parsedKey, stringValue)
			//	return stringValue, nil
			//} else if intValue, ok := v.(int); ok {
			//	return string(intValue), nil
			//} else if intValue, ok := v.(int64); ok {
			//	return string(intValue), nil
			//} else if floatValue, ok := v.(float64); ok {
			//	return fmt.Sprintf("%f", floatValue), nil

		} else {
			log.Printf("\033[31m[ERROR]\033[0m Schemaless reverse (7): Key '%s' found, but value is NOT a map[string]interface{}: %v. VALUE: '%s'", parsedKey, reflect.TypeOf(v), v)
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
func handleMultiListItems(translatedInput []interface{}, parentKey string, parsedValues map[string]interface{}, listDepth, childIndex int) []interface{} {
	if !strings.Contains(parentKey, ".") && len(translatedInput) == 0 {
		translatedInput = append(translatedInput, parsedValues)
	}

	// Start recursing to find the valid keys
	// Checks:
	// list -> subsub
	// maps -> childkeys
	// strings -> build it out.
	//for childKey, v := range parsedValues {

	if debug { 
		log.Printf("\n\n[DEBUG] STARTING NEW LIST\n\n")
	}

	newParsedValues := parsedValues
	for childKey, _ := range newParsedValues {
		if debug { 
			log.Printf("[DEBUG] CHILDKEY START (%d): %#v", childIndex, childKey)
		}

		v := parsedValues[childKey]
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

			// Handle list expansion
			if strings.Contains(val, "schemaless_list[") {

				foundList := strings.Split(val, "schemaless_list")
				if len(foundList) >= 2 {

					// Somehow ALWAYS look for the first INNER list
					// This makes it so that extrapolation can be done well across all fields everywhere
					matchingList := strings.Join(foundList[1:], "schemaless_list")
					newListStr := ""

					recording := false
					bracketCount := 0
					for _, matchChar := range matchingList {
						if matchChar == '[' {
							recording = true
							bracketCount += 1
							if bracketCount == 1 {
								//continue
							}
						}

						if recording {
							newListStr += string(matchChar)
						}

						if matchChar == ']' {
							bracketCount -= 1
							if bracketCount == 0 {
								recording = false
								break
							}
						}

						
					}

					if strings.HasPrefix(newListStr, "[") && strings.HasSuffix(newListStr, "]") {
						matchingList = newListStr
					}

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
					updated := false
					firstItem := modificationList[0]
					for cnt, listValue := range unmarshalledList {
						if cnt >= len(modificationList) {
							modificationList = append(modificationList, firstItem)
						}

						// Update the translatedInput with the new value
						newKey := fmt.Sprintf("%s.%s", newParentKey, childKey)
						newKey = strings.SplitN(newKey, ".", 2)[1]

						newModList := modificationList[cnt].(map[string]interface{})
						marshalledMap, err := json.Marshal(newModList)
						if err == nil {
							// FIXME: This part is going wrong with additional items in there
							//comparisonString := fmt.Sprintf(`"%s":"schemaless_list[`, newKey)
							//if !strings.Contains(string(marshalledMap), comparisonString) {

							if strings.Contains(string(marshalledMap), fmt.Sprintf(`"%s":"`, newKey)) && strings.Contains(string(marshalledMap), `schemaless_list[`) { 
								// Replace the schemaless_list[...] part with the listValue
								listValue = strings.Replace(val, fmt.Sprintf("schemaless_list%s", matchingList), listValue, 1)

							} else {
								continue
							}

							if debug { 
								log.Printf("[DEBUG] Listvalue to put '%s' in key '%s': %s", listValue, newKey, string(marshalledMap))
							}
						} else {
							log.Printf("[ERROR] Schemaless: Error marshalling map during list handling: %v", err)
						}

						newModList, _ = setNestedMap(newModList, newKey, listValue)
						if debug { 
							log.Printf("[DEBUG] NEW VALUE: %#v", newModList)
						}

						modificationList[cnt] = newModList
						updated = true
						//break
					}

					// FIXME: Something with parsedValues being overridden
					// every single time. Prolly in setNestedMap() or something
					if updated {
						parsedValues = modificationList[childIndex].(map[string]interface{})
					}

					marshalled, _ := json.MarshalIndent(modificationList, "", "\t")
					if debug { 
						log.Printf("[DEBUG] MARSHALLED (%d): %s.", listDepth, string(marshalled))
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

						updated = false
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
							newMap, _ = setNestedMap(newMap, oldParentKey, modificationList)
							translatedInput[len(translatedInput)-1] = newMap
						}

						parsedValues = modificationList[childIndex].(map[string]interface{})

					} else {
						parsedValues = modificationList[childIndex].(map[string]interface{})

						translatedInput = modificationList
					}

					marshalled, _ = json.MarshalIndent(translatedInput, "", "\t")
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

func getParsedMatch(match string) string {
	match = strings.TrimSpace(match)
	if strings.HasPrefix(match, "$") {
		match = strings.TrimPrefix(match, "$")
	}

	if strings.HasPrefix(match, "$.") {
		match = strings.TrimPrefix(match, "$.")
	}

	if strings.HasSuffix(match, ".") {
		match = strings.TrimSuffix(match, ".")
	}

	return match
}

func runJsonTranslation(ctx context.Context, inputValue []byte, translation map[string]interface{}, keepOriginal ...bool) ([]byte, []byte, error) {
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
	keepOriginalMapped := false
	if len(keepOriginal) > 0 {
		keepOriginalMapped = keepOriginal[0] 
	}

	// FIXME: Re-enable for simplicity
	if keepOriginalMapped { 
		translatedInput["unmapped"] = parsedInput
	}

	if !debug { 
		if keepOriginalMapped { 
			translatedInput["unmapped"] = parsedInput
		}
	} else {
		log.Printf("[DEBUG] Skipping unmapped for debug simplicity.")
	}

	for translationKey, translationValue := range translation {

		// Find the field in the parsedInput
		found := false
		for inputKey, inputValue := range parsedInput {
			if inputKey != translationValue {
				continue
			}

			//log.Printf("Found field %#v in input", inputKey)
			modifiedParsedInput[translationKey] = inputValue

			// Add the translated field to the translatedInput
			translatedInput[translationKey] = inputValue
			found = true
		}

		if found {
			if debug {
				log.Printf("[DEBUG] Schemaless: Direct match found for key '%s' with value '%v'", translationKey, translationValue)
			}
		} else {
			// Skipping (for now?)
			if _, ok := translationValue.(float64); ok {
				if debug {
					//log.Printf("[DEBUG] NOT handling float/integer translations and keeping value instead. Key: %s, Value: %v", translationKey, translationValue)
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
							} else {
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

					output, _, err := runJsonTranslation(ctx, inputValue, newValue, keepOriginalMapped)
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
						if debug {
							log.Printf("\n\n\nLIST: %s\n\n\n", string(output))
						}

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
				translation, _, err := runJsonTranslation(ctx, inputValue, val, keepOriginalMapped)
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
				if debug { 
					log.Printf("[DEBUG] Schemaless: Looking for field %#v in input field %#v", translationValue, translationKey)
				}

				// Basic, default translator
				//if strings.Contains(val, "[") {
				if strings.Contains(val, "[") || strings.Contains(val, "$") {
					fields := []Valuereplace{
						Valuereplace{
							Value: val,
						},
					}

					fields = TranslateBadFieldFormats(fields, true) 
					if len(fields) == 1 {
						if debug { 
							//log.Printf("[DEBUG] Schemaless: Translated bad field format for key '%s': '%s' -> '%s'", translationKey, val, fields[0].Value)
						}

						val = fields[0].Value
					} else {
						log.Printf("[ERROR] Schemaless: Unexpected number of fields after TranslateBadFieldFormats for key '%s': %d", translationKey, len(fields))
					}

					if debug { 
						log.Printf("[DEBUG] VAL: %#v", val)
					}
				}

				if strings.Contains(val, ".") || strings.Contains(val, "$") {
					// Check for ends with item.value[] <- array
					// This can't be handled properly without being something like .# or .#0
					if strings.Contains(val, "[]") {
						val = strings.ReplaceAll(val, "[]", ".#")
					}

					if strings.Contains(val, `"`) {
						val = strings.ReplaceAll(val, `"`, "")
					}

					// Specific parser for $
					if strings.Contains(val, "$") {
						newOutput := val

						// From app sdk => Same format.
						matchPattern := `([$]{1}([a-zA-Z0-9_@()-]+\.?){1}([a-zA-Z0-9#_@\()-]+\.?){0,})`
						// Find all occurrences
						re := regexp.MustCompile(matchPattern)
						matches := re.FindAllString(val, -1)
						for _, match := range matches {
							if debug { 
								log.Printf("[DEBUG] MATCH: %#v", match)
							}

							newParsedMatch := getParsedMatch(match)
							recursed, err := recurseFindKey(parsedInput, newParsedMatch, 0)
							if err != nil {
								log.Printf("[ERROR] Schemaless: Error in RecurseFindKey for match %#v: %v", match, err)
							}


							newOutput = strings.ReplaceAll(newOutput, match, recursed)
							if strings.Contains(match, ".#") {
								match = strings.ReplaceAll(match, ".#", "[]")
							}

							newOutput = strings.ReplaceAll(newOutput, match, recursed)
						}

						translatedInput[translationKey] = newOutput 
					} else {
						recursed, err := recurseFindKey(parsedInput, val, 0)
						if err != nil {
							if debug {
								log.Printf("[DEBUG] Schemaless Reverse problem: Error in RecurseFindKey for %#v: %v", val, err)
							}
						}

						translatedInput[translationKey] = recursed
					}

				} else {
					translatedInput[translationKey] = val
				}
			} else {
				log.Printf("[ERROR] Schemaless: Field %#v not found in input", translationValue)

				translatedInput[translationKey] = translationValue
			}

			// This is to do some custom parsing that makes 
			// schemaless["value1"] into the actual parent
			// Unsure if this breaks loop-in-loop or not (it may)
			if newVal, err := translatedInput[translationKey].(string); err {
				if strings.HasPrefix(newVal, "{{") && strings.HasSuffix(newVal, "}}") {
					newVal = strings.TrimPrefix(newVal, "{{")
					newVal = strings.TrimSuffix(newVal, "}}")
				}

				if strings.Contains(newVal, "schemaless_list[") && strings.HasSuffix(newVal, "]") {
					// Try to marshal it into a []string list
					newVal = strings.TrimPrefix(newVal, "schemaless_list")
					parsedList := []string{}
					err := json.Unmarshal([]byte(newVal), &parsedList)
					if err != nil {
						log.Printf("[ERROR] Schemaless: Error in unmarshalling schemaless_list for key '%s': %v", translationKey, err)
					} else {
						foundValue := ""
						for _, item := range parsedList {
							if len(item) == 0 {
								continue
							}

							foundValue = item

							// FIXME: Should not break until correct index is found
							// e.g. $items[3].value -> return the value for the correct index, not the first one in the list
							if len(foundValue) > 0 {
								break
							}
						}

						if len(foundValue) == 0 {
							translatedInput[translationKey] = "" 
						} else {
							translatedInput[translationKey] = foundValue 
						}
					}
				} else {
					if debug { 
						log.Printf("[ERROR] Schemaless: Value for key '%s' is a string but does not contain schemaless_list: %s", translationKey, newVal)
					}
				}
			} else {
				if debug {
					log.Printf("[DEBUG] Schemaless: Value for key '%s' is not a string, skipping schemaless_list parsing. Value: %#v", translationKey, translatedInput[translationKey])
				}
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
				if debug { 
					log.Printf("[DEBUG] Schemaless: Found a list in the mapJson. Should translate each item to the substandard. JSON Key: '%s'", k)
				}

				listJson = v.([]interface{})
				break
			}
		}
	}

	if len(listJson) == 0 {
		log.Printf("[DEBUG] Schemaless: No list key found in the sub body (1 LEVEL ONLY). No parsing to be done - returning empty list")
		return []byte(`[]`), nil
	}

	if debug { 
		log.Printf("[DEBUG] Schemaless: Found a list of length %d in the returnJson. Should translate each item to the substandard", len(listJson))
	}

	// For each item in the list, translate it to the substandard
	// Doing it with recursive Translate() calls
	skipAfterCount := 100 
	var wg sync.WaitGroup
	var mu sync.Mutex // Mutex to safely access parsedOutput slice

	parsedOutput := [][]byte{}
	for cnt, listItem := range listJson {

		// Skip: No goroutine on the first ones as we want to make sure caching is done properly before goroutining the rest. Prevents duplicates (mostly)
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
			time.Sleep(2 * time.Second) // Sleep for a bit to allow caching to be done properly before goroutining the rest
			continue
		}

		wg.Add(1) 
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

		time.Sleep(100 * time.Millisecond) // Sleep for a bit to prevent overwhelming the system with too many goroutines at once (also fixes caching)
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
	// keepOriginal (keep unstructured in blob)
	// URL
	// Authorization
	// OrgId
	// ExecutionId

	shuffleConfig := ShuffleConfig{}

	keepOriginal := false
	if len(inputConfig) > 0 {
		parsedInput := strings.Split(inputConfig[0], ",")
		for cnt, config := range parsedInput {
			if cnt == 0 {
				keepOriginal = (config == "true" || config == "1" || config == "yes")
			} else if cnt == 1 {
				shuffleConfig.URL = config
			} else if cnt == 2 {
				shuffleConfig.Authorization = config
			} else if cnt == 3 {
				shuffleConfig.OrgId = config
			} else if cnt == 4 {
				shuffleConfig.ExecutionId = config
			} else {
				log.Printf("[ERROR] Schemaless: Too many arguments for shuffleConfig (%d)", len(parsedInput))
				break
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
	if debug {
		log.Printf("[DEBUG] Schemaless: Getting existing structure for keyToken: '%s'", keyTokenFile)
	}

	inputStructure, inputStructErr := GetExistingStructure(keyTokenFile, shuffleConfig)
	fixedOutput := FixTranslationStructure(string(inputStructure))
	inputStructure = []byte(fixedOutput)
	if inputStructErr == nil {
		if debug {
			log.Printf("[DEBUG] Schemaless: Found existing structure for keyToken: '%s': %s", keyTokenFile, string(inputStructure))
		}
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

		trimmedStandard := strings.TrimSpace(string(standardFormat))
		if !skipSubstandard && len(trimmedStandard) > 2 && strings.HasPrefix(trimmedStandard, "[") && strings.HasSuffix(trimmedStandard, "]") {

			standardName := strings.TrimSuffix(strings.TrimPrefix(trimmedStandard, "["), "]")
			log.Printf("[DEBUG] Schemaless: Found a JSON array in the standard. Should convert it to a map[string]interface{}. Name: %s", standardName)
			_, err := GetStandard(standardName, shuffleConfig)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in GetSubStandard for standard %#v used for lists/standard references references: %v", standardName, err)
				return []byte{}, err
			}

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
		} else if !skipSubstandard && strings.HasSuffix(trimmedStandard, ".json") {
			log.Printf("[INFO] Side-loading substandard %s", trimmedStandard)

			_, err := GetStandard(trimmedStandard, shuffleConfig)
			if err != nil {
				log.Printf("[ERROR] Schemaless: Error in GetSubStandard for standard %#v used for lists/standard references references: %v", trimmedStandard, err)
				return []byte{}, err
			}
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

		err = SaveTranslation(keyTokenFile, gptTranslated, shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Problem in SaveTranslation (3): %v", err)
			return []byte{}, err
		}

		inputStructure = []byte(gptTranslated)
	}

	if debug {
		log.Printf("[DEBUG] Schemaless: Using inputStructure for keyToken '%s': %s", keyTokenFile, string(inputStructure))
	}

	// FIXME: Why was this cache stuff implemented? This is confusing 
	err = SetStructureCache(ctx, keyToken, inputStructure)
	if err != nil {
		log.Printf("[WARNING] Schemaless: problem in SetStructureCache for keyToken %#v with inputStructure %#v: %v", keyToken, inputStructure, err)
	}


	returnStructure, cacheErr := GetStructureFromCache(ctx, keyToken)
	if cacheErr != nil {
		log.Printf("[WARNING] Schemaless: problem in return structure for keyToken %#v. Should run ai and set cache!", keyToken)

		returnStructure = map[string]interface{}{}
		fixedCache := FixTranslationStructure(string(inputStructure))
		err = json.Unmarshal([]byte(fixedCache), &returnStructure)
		if err != nil {
			log.Printf("[ERROR] Schemaless: Error in unmarshal of returnStructure from cache for keyToken (2) %#v: %v", keyToken, err)
			//return []byte{}, err
		}
	}

	if debug {
		log.Printf("[DEBUG] Starting JSON translation with structure: %#v", returnStructure)
	}

	translation, modifiedInput, err := runJsonTranslation(ctx, []byte(startValue), returnStructure, keepOriginal)
	if err != nil {
		log.Printf("[ERROR] Error in runJsonTranslation: %v", err)
		return translation, err
	}

	_ = modifiedInput

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

func main() {

	/*
	startValue := `Services:
-   Orders:
    -   ID: $save ID1
        SupplierOrderCode: $SupplierOrderCode
    -   ID: $save ID2
        SupplierOrderCode: 111111`
	*/

	startValue := []byte(`{"properties": {"Name":{"id":"title","type":"title","title":[{"type":"text","text":{"content":"Everllence SE","link":null},"annotations":{"bold":false,"italic":false,"strikethrough":false,"underline":false,"code":false,"color":"default"},"plain_text":"Everllence SE","href":null}]}},"url":"https://www.notion.so/Everllence-SE-2ffa4982a6e780f09795eb7a379710dc","public_url":null}`)
	allStandards := []string{"ticket"}

	ctx := context.Background()
	output, err := Translate(ctx, allStandards[0], startValue)
	log.Printf("OUTPUT: %s, Err: %#v", string(output), err)
}
