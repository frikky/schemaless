package schemaless 

import (
	"os"
	"io"
	"log"
	"fmt"
	"time"
	"bytes"
	"errors"
	"context"
	"strings"
	"net/url"
	"net/http"
	"io/ioutil"
	//"math/rand"
	"crypto/tls"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"

	"github.com/bradfitz/slice"
	"golang.org/x/oauth2/google"
	"github.com/patrickmn/go-cache"
	"google.golang.org/appengine/memcache"
	"cloud.google.com/go/datastore"

	"github.com/shuffle/opensearch-go/v4/opensearchapi"
	opensearch "github.com/shuffle/opensearch-go/v4"
	gomemcache "github.com/bradfitz/gomemcache/memcache"
)

var mc = gomemcache.New(memcached)
var memcached = os.Getenv("SHUFFLE_MEMCACHED")
var requestCache = cache.New(60*time.Minute, 60*time.Minute)
var project Project

var maxCacheSize = 1020000

type File struct {
	Name string `json:"name"`
	Id string `json:"id"`
	Status string `json:"status"`
}

type Filestructure struct {
	Success bool `json:"success"`
	Namespaces []string `json:"namespaces"`
	List []File `json:"list"`
}

type Project struct { 
	Environment string `json:"environment"`
	CacheDb bool `json:"cache_db"`
	DbType string `json:"db_type"`

	Es opensearchapi.Client `json:"es"`
	Dbclient datastore.Client `json:"dbclient"`
}

func GetESIndexPrefix(index string) string {
	prefix := os.Getenv("SHUFFLE_OPENSEARCH_INDEX_PREFIX")
	if len(prefix) > 0 {
		return fmt.Sprintf("%s_%s", prefix, index)
	}

	return index
}

func init() {
	projectID := os.Getenv("SHUFFLE_GCEPROJECT")

	project.DbType = "datastore"
	project.Environment = "cloud"
	if len(projectID) == 0 {
		project.Environment = "onprem"
		project.DbType = "opensearch"

		project.Es = *GetEsConfig(false)
	} else {
		ctx := context.Background()
		dbclient, err := datastore.NewClient(ctx, projectID)
		if err != nil {
			log.Printf("[ERROR] Failed to get datastore client: %v", err)
		} else {
			project.Dbclient = *dbclient
		}
	}

	project.CacheDb = true
}

// Same as in shuffle-shared to make sure proxies are good
func GetExternalClient(baseUrl string) *http.Client {
	httpProxy := os.Getenv("HTTP_PROXY")
	httpsProxy := os.Getenv("HTTPS_PROXY")

	// Look for internal proxy instead
	// in case apps need a different one: https://jamboard.google.com/d/1KNr4JJXmTcH44r5j_5goQYinIe52lWzW-12Ii_joi-w/viewer?mtt=9r8nrqpnbz6z&f=0

	overrideHttpProxy := os.Getenv("SHUFFLE_INTERNAL_HTTP_PROXY")
	overrideHttpsProxy := os.Getenv("SHUFFLE_INTERNAL_HTTPS_PROXY")
	if len(overrideHttpProxy) > 0 && strings.ToLower(overrideHttpProxy) != "noproxy" {
		httpProxy = overrideHttpProxy
	}

	if len(overrideHttpsProxy) > 0 && strings.ToLower(overrideHttpProxy) != "noproxy" {
		httpsProxy = overrideHttpsProxy
	}

	transport := http.DefaultTransport.(*http.Transport)
	transport.MaxIdleConnsPerHost = 100
	transport.ResponseHeaderTimeout = time.Second * 60
	transport.IdleConnTimeout = time.Second * 60
	transport.Proxy = nil

	skipSSLVerify := false
	if strings.ToLower(os.Getenv("SHUFFLE_OPENSEARCH_SKIPSSL_VERIFY")) == "true" || strings.ToLower(os.Getenv("SHUFFLE_SKIPSSL_VERIFY")) == "true" { 
		skipSSLVerify = true

		os.Setenv("SHUFFLE_OPENSEARCH_SKIPSSL_VERIFY", "true")
		os.Setenv("SHUFFLE_SKIPSSL_VERIFY", "true")
	}

	transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS11,
		InsecureSkipVerify: skipSSLVerify,
	}

	if (len(httpProxy) > 0 || len(httpsProxy) > 0) && baseUrl != "http://shuffle-backend:5001" {
		//client = &http.Client{}
	} else {
		if len(httpProxy) > 0 {
			log.Printf("[INFO] Running with HTTP proxy %s (env: HTTP_PROXY)", httpProxy)

			url_i := url.URL{}
			url_proxy, err := url_i.Parse(httpProxy)
			if err == nil {
				transport.Proxy = http.ProxyURL(url_proxy)
			}
		}
		if len(httpsProxy) > 0 {
			log.Printf("[INFO] Running with HTTPS proxy %s (env: HTTPS_PROXY)", httpsProxy)

			url_i := url.URL{}
			url_proxy, err := url_i.Parse(httpsProxy)
			if err == nil {
				transport.Proxy = http.ProxyURL(url_proxy)
			}
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 60,
	}

	return client
}


type ShuffleConfig struct {
	URL string `json:"url"`
	OrgId string `json:"orgId"`

	Authorization string `json:"authorization"`
	ExecutionId string `json:"execution_id"`

	AuthenticationId string `json:"authentication_id"`
}

type FileStructure struct {
	Filename   string   `json:"filename"`
	OrgId      string   `json:"org_id"`
	WorkflowId string   `json:"workflow_id"`
	Namespace  string   `json:"namespace"`
	Tags       []string `json:"tags"`
}

type FileCreateResp struct {
	Success   bool `json:"success"`
	Id 		  string `json:"id"`
	Duplicate bool `json:"duplicate"`
}

type AuthenticationStore struct {
	Key   string `json:"key" datastore:"key"`
	Value string `json:"value" datastore:"value,noindex"`
}

type WorkflowApp struct { 
	Name string `json:"name" datastore:"name"`
}

type AppAuthenticationStorage struct {
	Active            bool                  `json:"active" datastore:"active"`
	Label             string                `json:"label" datastore:"label"`
	Id                string                `json:"id" datastore:"id"`
	App               WorkflowApp           `json:"app" datastore:"app,noindex"`
	Fields            []AuthenticationStore `json:"fields" datastore:"fields"`
	//Usage             []AuthenticationUsage `json:"usage" datastore:"usage"`
	WorkflowCount     int64                 `json:"workflow_count" datastore:"workflow_count"`
	NodeCount         int64                 `json:"node_count" datastore:"node_count"`
	OrgId             string                `json:"org_id" datastore:"org_id"`
	Created           int64                 `json:"created" datastore:"created"`
	Edited            int64                 `json:"edited" datastore:"edited"`
	Defined           bool                  `json:"defined" datastore:"defined"`
	Type              string                `json:"type" datastore:"type"`
	Encrypted         bool                  `json:"encrypted" datastore:"encrypted"`
	ReferenceWorkflow string                `json:"reference_workflow" datastore:"reference_workflow"`
	AutoDistribute    bool                  `json:"auto_distribute" datastore:"auto_distribute"`

	Environment string `json:"environment" datastore:"environment"` // In case an auth should ALWAYS be mapped to an environment. Can help out with Oauth2 refresh (e.g. running partially on cloud and partially onprem), as well as for KMS. For now ONLY KMS has a frontend.

	SuborgDistributed bool `json:"suborg_distributed" datastore:"suborg_distributed"` // Decides if it's distributed to suborgs or not

	SuborgDistribution []string `json:"suborg_distribution" datastore:"suborg_distribution"`

	//Validation TypeValidation `json:"validation" datastore:"validation"`
}

func AddShuffleFile(name, namespace string, data []byte, shuffleConfig ShuffleConfig) error { 
	if len(shuffleConfig.URL) < 1 {
		return errors.New("Shuffle URL not set when adding file")
	}

	if !strings.Contains(name, "json") {
		name = fmt.Sprintf("%s.json", name)
	}

	client := GetExternalClient(shuffleConfig.URL)
	fileUrl := fmt.Sprintf("%s/api/v1/files/create?unique=true", shuffleConfig.URL)
	fileData := FileStructure{
		Filename: name,
		Namespace: namespace,
	}

	if len(shuffleConfig.ExecutionId) > 0 {
		fileUrl += "&execution_id=" + shuffleConfig.ExecutionId
	}

	// Check if the file has already been uploaded based on shuffleConfig.OrgId+namespace+data. No point in overwriting with the same data.
	hasher := md5.New()
	ctx := context.Background()
	hasher.Write([]byte(fmt.Sprintf("%s%s%s%s", shuffleConfig.OrgId, name, namespace, string(data))))
	cacheKey := hex.EncodeToString(hasher.Sum(nil))
	cache, err := GetCache(ctx, cacheKey)
	if err == nil {
		cacheData := []byte(cache.([]uint8))
		if len(cacheData) > 0 { 
			return nil
		}
	}
	
	fileDataJson, err := json.Marshal(fileData)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		"POST", 
		fileUrl,
		bytes.NewBuffer(fileDataJson),
	)

	if err != nil {
		return err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", shuffleConfig.Authorization))
	if len(shuffleConfig.OrgId) > 0 {
		req.Header.Add("Org-Id", shuffleConfig.OrgId)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[ERROR] Schemaless (1): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	if resp.StatusCode != 200 {
		log.Printf("[ERROR] Schemaless: Bad status code (3) for %s: %s", fileUrl, resp.Status)
		return errors.New(fmt.Sprintf("Bad status code: %s", resp.Status))
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] Schemaless (2): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	// Unmarshal to FileCreateResp
	var fileCreateResp FileCreateResp
	err = json.Unmarshal(body, &fileCreateResp)
	if err != nil {
		log.Printf("[ERROR] Schemaless (3): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	if !fileCreateResp.Success {
		log.Printf("[ERROR] Schemaless (4): Error getting file %#v from Shuffle backend: %s", name, string(body))
		return errors.New(fmt.Sprintf("Failed adding shuffle file: %s", string(body)))
	}

	if fileCreateResp.Duplicate {
		//log.Printf("[INFO] Schemaless: File %#v already exists in Shuffle", name)
		return nil
	}

	// Upload file to the ID
	fileUploadUrl := fmt.Sprintf("%s/api/v1/files/%s/upload", shuffleConfig.URL, fileCreateResp.Id)

	if len(shuffleConfig.ExecutionId) > 0 {
		fileUploadUrl += "?execution_id=" + shuffleConfig.ExecutionId
	}

	// Handle file upload with correct content-type
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	fileField, err := writer.CreateFormFile("shuffle_file", name)
	if err != nil {
		log.Printf("[ERROR] Schemaless (5): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	// Create a ReadSeeker from the original data
	fileReader := bytes.NewReader(data)

	// Copy the data from the reader to the form field
	_, err = io.Copy(fileField, fileReader)
	if err != nil {
		log.Printf("[ERROR] Schemaless (6): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	// Close the multipart writer
	writer.Close()


    // Create a new form-data field with the original data
	req, err = http.NewRequest(
		"POST", 
		fileUploadUrl, 
		&requestBody,
	)
	if err != nil {
		log.Printf("[ERROR] Schemaless (5): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", shuffleConfig.Authorization))
	if len(shuffleConfig.OrgId) > 0 {
		req.Header.Add("Org-Id", shuffleConfig.OrgId)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "schemaless/1.0.0")
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("[ERROR] Schemaless (6): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	if resp.StatusCode != 200 {
		log.Printf("[ERROR] Schemaless: Bad status code (4) for %s: %s", fileUploadUrl, resp.Status)
		return errors.New(fmt.Sprintf("Bad status code: %s", resp.Status))
	}

	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] Schemaless (7): Error getting file %#v from Shuffle backend: %s", name, err)
		return err
	}

	// Update with basically nothing, as the point isn't to get the file itself
	err = SetCache(ctx, cacheKey, []byte("1"), 10)
	if err != nil {
		log.Printf("[ERROR] Schemaless (8): Error setting cache for file %#v from Shuffle backend: %s", name, err)
	}

	return nil
}

func GetShuffleFileById(id string, shuffleConfig ShuffleConfig) ([]byte, error) {
	if len(shuffleConfig.URL) < 1 {
		return []byte{}, errors.New("Shuffle URL not set")
	}

	client := GetExternalClient(shuffleConfig.URL)
	fileUrl := fmt.Sprintf("%s/api/v1/files/%s/content", shuffleConfig.URL, id)

	ctx := context.Background()
	var body []byte

	hasher := md5.New()
	hasher.Write([]byte(fileUrl+shuffleConfig.Authorization+shuffleConfig.OrgId+shuffleConfig.ExecutionId))
	cacheKey := hex.EncodeToString(hasher.Sum(nil))

	// The file will be grabbed a ton, hence the cache actually speeding things up and reducing requests

	cache, err := GetCache(ctx, cacheKey)
	if err == nil {
		body = []byte(cache.([]uint8))
		return body, nil
	}

	if len(shuffleConfig.ExecutionId) > 0 {
		fileUrl += "?execution_id=" + shuffleConfig.ExecutionId
	}

	req, err := http.NewRequest(
		"GET", 
		fileUrl,
		nil,
	)

	if err != nil {
		return []byte{}, err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", shuffleConfig.Authorization))
	if len(shuffleConfig.OrgId) > 0 {
		req.Header.Add("Org-Id", shuffleConfig.OrgId)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[ERROR] Schemaless (1): Error getting file %#v from Shuffle backend: %s", id, err)
		return []byte{}, err
	}

	defer resp.Body.Close()
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] Schemaless (2): Error reading file %#v from Shuffle backend: %s", id, err)
		return []byte{}, err
	}

	go SetCache(ctx, cacheKey, body, 10)
	if resp.StatusCode != 200 {
		log.Printf("[ERROR] Schemaless: Bad status code (1) for %s: %s", fileUrl, resp.Status)
		return []byte{}, errors.New(fmt.Sprintf("Bad status code when downloading file %s: %s", id, resp.Status))
	}

	return body, nil
}

// Finds a file in shuffle in a specified category
// The string return is the filepath OR the file ID, with priority on file ID.
func FindShuffleFile(name, category string, shuffleConfig ShuffleConfig) ([]byte, string, error) {
	filename := ""
	if len(shuffleConfig.URL) < 1 {
		return []byte{}, filename, errors.New("Shuffle URL not set")
	}


	// 1. Get the category 
	// 2. Find the file in the category output
	// 3. Read the file data
	// 4. Return it
	client := GetExternalClient(shuffleConfig.URL)

	// Specifically for handling default standards we deal with all the time
	newName := name
	if category == "translation_standards" && strings.HasPrefix(newName, "get_") {
		newName = strings.TrimPrefix(newName, "get_")
	}

	categoryUrl := fmt.Sprintf("%s/api/v1/files/namespaces/%s?ids=true&filename=%s", shuffleConfig.URL, category, newName)

	hasher := md5.New()
	hasher.Write([]byte(categoryUrl+shuffleConfig.Authorization+shuffleConfig.OrgId+shuffleConfig.ExecutionId))
	cacheKey := hex.EncodeToString(hasher.Sum(nil))

	// Get the cache 
	ctx := context.Background()
	var body []byte
	cache, err := GetCache(ctx, cacheKey)
	if err == nil {
		//log.Printf("[INFO] Schemaless: FOUND file %#v in category %#v from cache", name, category)
		body = []byte(cache.([]uint8))
		//return cacheData, filename, nil
	} else {
		if debug { 
			log.Printf("[DEBUG] Schemaless: Finding file %#v in category %#v from Shuffle backend", newName, category)
		}

		if len(shuffleConfig.ExecutionId) > 0 {
			categoryUrl += "&execution_id=" + shuffleConfig.ExecutionId
		}

		if len(shuffleConfig.Authorization) > 0 {
			categoryUrl += "&authorization=" + shuffleConfig.Authorization
		}

		if debug { 
			log.Printf("[DEBUG] Getting category WITHOUT cache from '%s'", categoryUrl)
		}

		req, err := http.NewRequest(
			"GET", 
			categoryUrl,
			nil,
		)

		if err != nil {
			log.Printf("[ERROR] Schemaless (2): Error getting category %#v from Shuffle backend: %s", category, err)
			return []byte{}, filename, err
		}

		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", shuffleConfig.Authorization))
		if len(shuffleConfig.OrgId) > 0 {
			req.Header.Add("Org-Id", shuffleConfig.OrgId)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[ERROR] Schemaless (3): Error getting category %#v from Shuffle backend: %s", category, err)
			return []byte{}, filename, err
		}


		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[ERROR] Schemaless (4): Error reading category %#v from Shuffle backend: %s", category, err)
			return []byte{}, filename, err
		}

		go SetCache(ctx, cacheKey, body, 3)
		if resp.StatusCode != 200 {
			log.Printf("[ERROR] Schemaless: Bad status code (2) getting category %#v from Shuffle backend %#v: %s", category, categoryUrl, resp.Status)
			return []byte{}, filename, errors.New(fmt.Sprintf("Bad status code: %s", resp.Status))
		}

		if debug { 
			log.Printf("[DEBUG] Schemaless: Got category %#v from Shuffle backend. Resp: %d", category, resp.StatusCode)
		}
	}

	// Unmarshal to Filestructure struct
	files := Filestructure{}
	err = json.Unmarshal(body, &files)
	if err != nil {
		log.Printf("[ERROR] Schemaless (5): Error unmarshalling category %#v from Shuffle backend: %s", category, err)
		return []byte{}, filename, err
	}

	newName = strings.TrimSpace(strings.ToLower(strings.Replace(newName, " ", "_", -1)))
	if strings.HasSuffix(newName, ".json") {
		newName = newName[:len(name)-5]
	}

	for _, file := range files.List {
		if file.Status != "active" {
			continue
		}

		innerfilename := strings.TrimSpace(strings.ToLower(strings.Replace(file.Name, " ", "_", -1)))
		if strings.HasSuffix(innerfilename, ".json") {
			innerfilename = innerfilename[:len(innerfilename)-5]
		}

		if innerfilename != newName { 
			continue
		}

		filename = innerfilename
		downloadedFile, err := GetShuffleFileById(file.Id, shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless (6): Error getting file %#v from Shuffle backend: %s", newName, err)
			return []byte{}, filename, err
		}

		// This is the important part. Returning an ID is perfect
		return downloadedFile, file.Id, nil
	}

	// Validation
	//if debug { 
	//	log.Printf("File search: %s", newName)
	//	log.Printf("FILES: %d", len(files.List))
	//	log.Printf("BODY: %s", body)
	//	os.Exit(3)
	//}

	return []byte{}, filename, errors.New(fmt.Sprintf("Failed to find translation file matching name '%s' in category '%s'", newName, category)) 
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

func GetGeminiCredentials(ctx context.Context) (string, string, string) { 
	foundModel := "google/gemini-3.7-flash"  

	projectID := os.Getenv("SHUFFLE_GCEPROJECT")
	if len(projectID) == 0 { 
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}

	location := os.Getenv("SHUFFLE_GCE_LOCATION")
	if len(projectID) == 0 || len(location) == 0 {
		return "", "", foundModel
	}

	// 1. Get Application Default Credentials (ADC) with Cloud Platform scope
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		log.Printf("[ERROR] Failed to find GCP credentials: %v", err)
		return "", "", foundModel
	}

	// 2. TokenSource caches tokens in memory automatically.
	// Reuse this tokenSource across your entire application lifecycle.
	tok, err := creds.TokenSource.Token()
	if err != nil {
		log.Printf("[ERROR] Failed to get token from TokenSource: %v", err)
		return "", "", foundModel
	}

	parsedUrl := fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi", projectID, location)
	return tok.AccessToken, parsedUrl, foundModel
}

func GetOrgAiCredentials(ctx context.Context, callInfo ShuffleConfig) (string, string, string) {

	orgId := callInfo.OrgId
	if len(orgId) == 0 {
		return "", "", ""
	}

	apiKey := ""
	aiRequestUrl := ""
	foundModel := ""

	// This is cached hence should be fast enough
	auths, err := GetAllWorkflowAppAuth(ctx, orgId)
	if err != nil {
		log.Printf("[ERROR] Failed to get workflow app auths for org %s: %s", orgId, err)
		return apiKey, aiRequestUrl, foundModel
	}

	// Sort by editing time
	slice.Sort(auths[:], func(i, j int) bool {
		return auths[i].Edited > auths[j].Edited
	})

	for _, auth := range auths {
		if len(callInfo.AuthenticationId) > 0 && auth.Id != callInfo.AuthenticationId {
			continue
		}

		// Disallowing unactive auth, as that's how we control which to use
		// If authId is specified, we don't care.
		if len(callInfo.AuthenticationId) == 0 && auth.Active == false {
			continue
		}

		// FIXME: Do we need this? Keeping for now, as to sort it properly
		// This is specifcally to keep the OpenAI request format.
		if strings.ToLower(auth.App.Name) != "openai" {
			continue
		}

		curApiKey := ""
		curUrl := ""
		curModel := ""

		for _, field := range auth.Fields {
			// Check if the auth has a valid API key
			if field.Key == "apikey" {
				parsedKey := fmt.Sprintf("%s_%d_%s_%s", auth.OrgId, auth.Created, auth.Label, field.Key)
				decrypted, err := HandleKeyDecryption([]byte(field.Value), parsedKey)
				if err == nil {
					curApiKey = string(decrypted)
				}
			}

			if field.Key == "url" {
				parsedKey := fmt.Sprintf("%s_%d_%s_%s", auth.OrgId, auth.Created, auth.Label, field.Key)
				decrypted, err := HandleKeyDecryption([]byte(field.Value), parsedKey)
				if err == nil {
					curUrl = string(decrypted)
				}
			}

			if field.Key == "model" {
				parsedKey := fmt.Sprintf("%s_%d_%s_%s", auth.OrgId, auth.Created, auth.Label, field.Key)
				decrypted, err := HandleKeyDecryption([]byte(field.Value), parsedKey)
				if err == nil {
					curModel = string(decrypted)
				}
			}
		}

		// Custom URL must only be used when paired with an API key
		if len(curUrl) > 0 && len(curApiKey) == 0 {
			curUrl = ""
		}

		if len(curApiKey) > 0 {
			apiKey = curApiKey
			aiRequestUrl = curUrl
			foundModel = curModel
		}

		// openai auth.Active is the primary one at all times
		//if auth.Validation.Valid && len(apiKey) > 0 && len(aiRequestUrl) > 0 {
		//	break
		//}
	}

	// Handles failover IF we can't find other auth.
	// Only applies to on-prem deployments - cloud never needs to fail over to itself.
	if project.Environment == "onprem" && (apiKey == "" || aiRequestUrl == "") {

		if debug {
			log.Printf("[DEBUG] No custom LLM-credentials found for org %s. Falling back to Cloud Sync AI endpoint IF cloud sync is enabled.", orgId)
		}

		org, err := GetOrg(ctx, orgId)
		if err != nil {
			log.Printf("[ERROR] Failed to get org by ID %s: %s", orgId, err)
			return apiKey, aiRequestUrl, foundModel
		}
		if len(org.CreatorOrg) > 0 {
			org, err = GetOrg(ctx, org.CreatorOrg)
			if err == nil && len(org.SyncConfig.Apikey) > 0 {
				apiKey = org.SyncConfig.Apikey
			}
		}

		// Checks if cloud sync is set up
		if len(org.SyncConfig.Apikey) > 0 {
			apiKey = org.SyncConfig.Apikey
		} else {
			return "", "", ""
		}

		baseUrl := "https://uk.shuffler.io"
		if len(org.SyncConfig.URL) > 0 && (strings.HasPrefix(org.SyncConfig.URL, "https://") || strings.HasPrefix(org.SyncConfig.URL, "http://")) {
			baseUrl = strings.TrimSuffix(org.SyncConfig.URL, "/")
		}

		regionUrlCacheKey := fmt.Sprintf("org_cloudsync_region_url_%s", orgId)
		if cached, cacheErr := GetCache(ctx, regionUrlCacheKey); cacheErr == nil {
			cachedUrl := ""
			switch typed := cached.(type) {
			case []byte:
				cachedUrl = string(typed)
			case string:
				cachedUrl = typed
			}

			if len(cachedUrl) > 0 && (strings.HasPrefix(cachedUrl, "https://") || strings.HasPrefix(cachedUrl, "http://")) {
				baseUrl = strings.TrimSuffix(cachedUrl, "/")
			}
		}

		aiRequestUrl = fmt.Sprintf("%s/api/v1", baseUrl)
	}

	// To avoid recursion of self-requesting backing to the same endpoint
	if project.Environment == "cloud" && (strings.Contains(aiRequestUrl, "shuffler.io") || (strings.Contains(aiRequestUrl, "shuffle") && strings.Contains(aiRequestUrl, "app.run"))) {
		return "", "", ""
	}

	// Defense-in-depth: custom URL must never be returned without an accompanying API key
	if len(aiRequestUrl) > 0 && len(apiKey) == 0 {
		aiRequestUrl = ""
	}

	return apiKey, aiRequestUrl, foundModel
}

func GetAllWorkflowAppAuth(ctx context.Context, orgId string) ([]AppAuthenticationStorage, error) {
	var allworkflowappAuths []AppAuthenticationStorage
	nameKey := "workflowappauth"

	cacheKey := fmt.Sprintf("%s_%s", nameKey, orgId)
	if project.CacheDb {
		cache, err := GetCache(ctx, cacheKey)
		if err == nil {
			cacheData := []byte(cache.([]uint8))
			err = json.Unmarshal(cacheData, &allworkflowappAuths)
			if err == nil || len(allworkflowappAuths) > 0 {
				return allworkflowappAuths, nil
			}
		} else {
			//log.Printf("[DEBUG] Failed getting cache for app auth: %s", err)
		}
	}

	if project.DbType == "opensearch" {
		//log.Printf("GETTING ES USER %s",
		var buf bytes.Buffer
		query := map[string]interface{}{
			"size": 1000,
			"query": map[string]interface{}{
				"match": map[string]interface{}{
					"org_id": orgId,
				},
			},
		}

		if err := json.NewEncoder(&buf).Encode(query); err != nil {
			log.Printf("[WARNING] Error encoding find user query: %s", err)
			return allworkflowappAuths, err
		}

		resp, err := project.Es.Search(ctx, &opensearchapi.SearchReq{
			Indices: []string{strings.ToLower(GetESIndexPrefix(nameKey))},
			Body:    &buf,
			Params: opensearchapi.SearchParams{
				TrackTotalHits: true,
			},
		})
		if err != nil {
			if strings.Contains(err.Error(), "index_not_found_exception") {
				return allworkflowappAuths, nil
			}

			log.Printf("[ERROR] Error getting response from Opensearch (get app auth): %s", err)
			return allworkflowappAuths, err
		}

		res := resp.Inspect().Response
		defer res.Body.Close()
		if res.StatusCode == 404 {
			return allworkflowappAuths, nil
		}

		if res.IsError() {
			var e map[string]interface{}
			if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
				log.Printf("[WARNING] Error parsing the response body: %s", err)
				return allworkflowappAuths, err
			} else {
				// Print the response status and error information.
				log.Printf("[%s] %s: %s",
					res.Status(),
					e["error"].(map[string]interface{})["type"],
					e["error"].(map[string]interface{})["reason"],
				)
			}
		}

		if res.StatusCode != 200 && res.StatusCode != 201 {
			return allworkflowappAuths, errors.New(fmt.Sprintf("Bad statuscode: %d", res.StatusCode))
		}

		respBody, err := ioutil.ReadAll(res.Body)
		if err != nil {
			return allworkflowappAuths, err
		}

		wrapped := AppAuthSearchWrapper{}
		err = json.Unmarshal(respBody, &wrapped)
		if err != nil {
			return allworkflowappAuths, err
		}

		allworkflowappAuths = []AppAuthenticationStorage{}
		for _, hit := range wrapped.Hits.Hits {
			allworkflowappAuths = append(allworkflowappAuths, hit.Source)
		}
	} else {
		q := datastore.NewQuery(nameKey).Filter("org_id = ", orgId)
		if orgId == "ALL" && project.Environment != "cloud" {
			q = datastore.NewQuery(nameKey)
		}

		_, err := project.Dbclient.GetAll(ctx, q, &allworkflowappAuths)
		if err != nil && len(allworkflowappAuths) == 0 {
			if !strings.Contains(err.Error(), `cannot load field`) {

				if project.CacheDb {
					data, err := json.Marshal(allworkflowappAuths)
					if err != nil {
						log.Printf("[WARNING] Failed marshalling get app auth (2): %s", err)
						return allworkflowappAuths, nil
					}

					err = SetCache(ctx, cacheKey, data, 10)
					if err != nil {
						log.Printf("[WARNING] Failed updating get app auth cache (2): %s", err)
					}
				}

				return allworkflowappAuths, err
			}
		}
	}

	// Should check if it's a child org and get parent orgs app auths that are shared
	foundOrg, err := GetOrg(ctx, orgId)
	if err == nil && len(foundOrg.ChildOrgs) == 0 && len(foundOrg.CreatorOrg) > 0 && foundOrg.CreatorOrg != orgId {

		parentOrg, err := GetOrg(ctx, foundOrg.CreatorOrg)
		if err == nil {

			// No recursion as parents can't have parents
			parentAuths, err := GetAllWorkflowAppAuth(ctx, parentOrg.Id)
			if err == nil {
				for _, parentAuth := range parentAuths {
					if !parentAuth.SuborgDistributed && !ArrayContains(parentAuth.SuborgDistribution, orgId) {
						continue
					}

					allworkflowappAuths = append(allworkflowappAuths, parentAuth)
				}
			}
		}
	}

	// Deduplicate keys
	for _, auth := range allworkflowappAuths {
		allFields := []string{}
		newFields := []AuthenticationStore{}
		for _, field := range auth.Fields {
			if ArrayContains(allFields, field.Key) {
				continue
			}

			allFields = append(allFields, field.Key)
			newFields = append(newFields, field)
		}

		auth.Fields = newFields
	}

	if project.CacheDb {
		data, err := json.Marshal(allworkflowappAuths)
		if err != nil {
			log.Printf("[WARNING] Failed marshalling get app auth: %s", err)
			return allworkflowappAuths, nil
		}

		err = SetCache(ctx, cacheKey, data, 30)
		if err != nil {
			log.Printf("[WARNING] Failed updating get app auth cache: %s", err)
		}
	}

	//for _, env := range allworkflowappAuths {
	//	for _, param := range env.Fields {
	//		log.Printf("ENV: %s", param)
	//	}
	//}

	return allworkflowappAuths, nil
}

func HandleKeyDecryption(data []byte, passphrase string) ([]byte, error) {
	//if debug {
	//	log.Printf("[DEBUG] Passphrase: %s", passphrase)
	//	log.Printf("Decrypting key: %s", data)
	//}

	key, err := create32Hash(passphrase)
	if err != nil {
		log.Printf("[ERROR] Failed hashing in decrypt: %s", err)
		return []byte{}, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		log.Printf("[ERROR] Error creating cipher from key in decryption: %s", err)
		return []byte{}, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("[ERROR] Error creating new GCM block in decryption: %s", err)
		return []byte{}, err
	}

	parsedData, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		//log.Printf("[WARNING] Failed base64 decode for auth key '%s': '%s'. Data: '%s'. Returning as if this is valid.", data, err, string(data))
		//return []byte{}, err
		return data, nil
	}

	nonceSize := gcm.NonceSize()
	if nonceSize > len(parsedData) {
		//log.Printf("[ERROR] Nonce size is larger than parsed data in decryption. Returning as if this is valid. This should _never_ happen, but typically only happens IF the source data is invalid (e.g. 1/20 keys)")
		//if debug {
		//	log.Printf("Returned: '%s'. Len %d vs %d", string(parsedData), nonceSize, len(parsedData))
		//}

		return data, nil
	}

	nonce, ciphertext := parsedData[:nonceSize], parsedData[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		//log.Printf("[ERROR] Error reading decryptionkey: %s - nonce: %s, ciphertext: %s", err, nonce, ciphertext)
		//log.Printf("[ERROR] Error reading decryptionkey: %s - nonce: %s", err, nonce)
		return []byte{}, err
	}

	return plaintext, nil
}

// Handles org grabbing and user / org migrations
func GetOrg(ctx context.Context, id string) (*Org, error) {
	if id == "public" {
		//return &Org{}, errors.New("'public' org is used for Singul action without being logged in. Not relevant.")
		return &Org{
			Id:   "public",
			Name: "Public",
		}, nil
	}

	// Clean the ID: remove whitespace, quotes, and backslashes
	originalId := id
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "\"", "")
	id = strings.ReplaceAll(id, "'", "")
	id = strings.ReplaceAll(id, "\\", "")
	if len(id) == 0 {
		return &Org{}, errors.New("Empty org id after cleaning")
	}
	if id != originalId {
		log.Printf("[WARNING] GetOrg ID was cleaned from '%s' to '%s' - check data source", originalId, id)
	}

	nameKey := "Organizations"
	cacheKey := fmt.Sprintf("%s_%s", nameKey, id)
	curOrg := &Org{}
	if project.CacheDb {
		cache, err := GetCache(ctx, cacheKey)
		if err == nil {
			cacheData := []byte(cache.([]uint8))
			err = json.Unmarshal(cacheData, curOrg)
			if err == nil {
				if curOrg.Id == "" {
					return curOrg, errors.New("Org doesn't exist")
				} else {
					return curOrg, nil
				}
			}
		} else {
			//log.Printf("[DEBUG] Failed getting cache for org %s (2): %s", id, err)
		}
	}

	if project.DbType == "opensearch" {
		if len(id) == 0 {
			return &Org{}, errors.New("Empty org id")
		}

		resp, err := project.Es.Document.Get(ctx, opensearchapi.DocumentGetReq{
			Index:      strings.ToLower(GetESIndexPrefix(nameKey)),
			DocumentID: id,
		})
		if err != nil {
			log.Printf("[WARNING] Error in org get: %s", err)
			return &Org{}, err
		}

		res := resp.Inspect().Response
		defer res.Body.Close()
		respBody, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Printf("[WARNING] Failed getting org body: %s", err)
			return &Org{}, err
		}

		if res.StatusCode == 404 {
			log.Printf("[WARNING] Failed getting org '%s' - status: 404 - %s", id, string(respBody))
			return &Org{}, errors.New("Org doesn't exist")
		}

		wrapped := OrgWrapper{}
		err = json.Unmarshal(respBody, &wrapped)
		if err != nil {
			log.Printf("[WARNING] Failed unmarshaling org: %s", err)
			return &Org{}, err
		}

		curOrg = &wrapped.Source
	} else {
		key := datastore.NameKey(nameKey, id, nil)
		var getErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					getErr = errors.New("datastore client not initialized")
				}
			}()
			if err := project.Dbclient.Get(ctx, key, curOrg); err != nil {
				getErr = err
			}
		}()
		if err := getErr; err != nil {
			if strings.Contains(err.Error(), `cannot load field`) {
				log.Printf("[WARNING] Error in org loading (4), but returning without warning: %s", err)
				err = nil
			} else {
				if strings.Contains(err.Error(), `no such entity`) && project.CacheDb {
					neworg, err := json.Marshal(curOrg)
					if err != nil {
						return &Org{}, err
					}

					// Set cache for it
					err = SetCache(ctx, cacheKey, neworg, 30)
					if err != nil {
						log.Printf("[ERROR] Failed updating org cache (3): %s", err)
					}
				} else {
					log.Printf("[ERROR] Problem in org loading (2) for %s: %s", key, err)
				}

				//orgErr = err
				return &Org{}, err
			}
		}
	}

	// How does this happen?
	if len(curOrg.Id) == 0 {
		curOrg.Id = id
		//return curOrg, errors.New(fmt.Sprintf("Couldn't find org with ID '%s'", curOrg.Id))
	}

	// Check if Subscription is from BEFORE November 4th 2023
	if project.CacheDb {
		neworg, err := json.Marshal(curOrg)
		if err != nil {
			log.Printf("[ERROR] Failed marshalling org for cache: %s", err)
			return curOrg, nil
		}

		err = SetCache(ctx, cacheKey, neworg, 1440)
		if err != nil {
			log.Printf("[ERROR] Failed updating org cache: %s", err)
		}
	}

	return curOrg, nil
}

// Uses a simple way to be able to modify the encryption key being used
func create32Hash(key string) ([]byte, error) {
	encryptionModifier := os.Getenv("SHUFFLE_ENCRYPTION_MODIFIER")
	if len(encryptionModifier) == 0 {
		return []byte{}, errors.New(fmt.Sprintf("No encryption modifier set. Define env SHUFFLE_ENCRYPTION_MODIFIER to some random string and NEVER change it to start using encrypted auth."))
	}

	key += encryptionModifier
	hasher := md5.New()
	hasher.Write([]byte(key))
	return []byte(hex.EncodeToString(hasher.Sum(nil))), nil
}

func ArrayContains(visited []string, id string) bool {
	found := false
	for _, item := range visited {
		if item == id {
			found = true
			break
		}
	}

	return found
}

type SyncConfig struct {
	URL      string `json:"url" datastore:"url"`
	Interval int64  `json:"interval" datastore:"interval"`
	Apikey   string `json:"api_key" datastore:"api_key"`
	Source   string `json:"source" datastore:"source"`

	WorkflowBackup bool `json:"workflow_backup" datastore:"workflow_backup"`
	AppBackup      bool `json:"app_backup" datastore:"app_backup"`
	AiCloudSync    bool `json:"ai_cloud_sync" datastore:"ai_cloud_sync"`

	WorkflowBackupUpdated int64 `json:"workflow_backup_updated" datastore:"workflow_backup_updated"`
	AppBackupUpdated      int64 `json:"app_backup_updated" datastore:"app_backup_updated"`
	AiCloudSyncUpdated    int64 `json:"ai_cloud_sync_updated" datastore:"ai_cloud_sync_updated"`
}

type Org struct {
	Name            string       `json:"name" datastore:"name"`
	Description     string       `json:"description" datastore:"description"`
	CompanyType     string       `json:"company_type" datastore:"company_type"`
	Image           string       `json:"image" datastore:"image,noindex"`
	Id              string       `json:"id" datastore:"id"`
	Org             string       `json:"org" datastore:"org"`
	//Users           []User       `json:"users" datastore:"users"`
	Role            string       `json:"role" datastore:"role"`
	Roles           []string     `json:"roles" datastore:"roles"`
	ActiveApps      []string     `json:"active_apps" datastore:"active_apps"`
	CloudSync       bool         `json:"cloud_sync" datastore:"CloudSync"`
	CloudSyncActive bool         `json:"cloud_sync_active" datastore:"CloudSyncActive"`
	SyncConfig      SyncConfig   `json:"sync_config" datastore:"sync_config"`
	//SyncFeatures    SyncFeatures `json:"sync_features,omitempty" datastore:"sync_features"`
	MFARequired     bool         `json:"mfa_required" datastore:"mfa_required"`

	SubscriptionUserId string                `json:"subscription_user_id" datastore:"subscription_user_id"`
	//Subscriptions      []PaymentSubscription `json:"subscriptions" datastore:"subscriptions"`

	//SyncUsage         SyncUsage   `json:"sync_usage" datastore:"sync_usage"`
	Created           int64       `json:"created" datastore:"created"`
	Edited            int64       `json:"edited" datastore:"edited"`
	//Defaults          Defaults    `json:"defaults" datastore:"defaults"`
	Invites           []string    `json:"invites" datastore:"invites"`
	ChildOrgs         []OrgMini   `json:"child_orgs" datastore:"child_orgs"`
	//ManagerOrgs       []OrgMini   `json:"manager_orgs" datastore:"manager_orgs"` // Multi in case more than one org should be able to control another
	//PartnerInfo       PartnerInfo `json:"partner_info" datastore:"partner_info"`
	//SSOConfig         SSOConfig   `json:"sso_config" datastore:"sso_config"`
	//SecurityFramework Categories  `json:"security_framework" datastore:"security_framework,noindex"`

	//Interests    []Priority `json:"interests" datastore:"interests"`
	//Priorities   []Priority `json:"priorities" datastore:"priorities,noindex"`
	MainPriority string     `json:"main_priority" datastore:"main_priority"`

	Region    string     `json:"region" datastore:"region"`
	RegionUrl string     `json:"region_url" datastore:"region_url"`
	//Tutorials []Tutorial `json:"tutorials" datastore:"tutorials"`
	//LeadInfo  LeadInfo   `json:"lead_info,omitempty" datastore:"lead_info"`
	//OrgAuth   OrgAuth    `json:"org_auth" datastore:"org_auth"`

	CreatorId string `json:"creator_id" datastore:"creator_id"`
	Disabled  bool   `json:"disabled" datastore:"disabled"`

	EulaSigned   bool        `json:"eula_signed" datastore:"eula_signed"`
	EulaSignedBy string      `json:"eula_signed_by" datastore:"eula_signed_by"`
	//Billing      Billing     `json:"Billing" datastore:"Billing"`
	CreatorOrg   string      `json:"creator_org" datastore:"creator_org"`
	//Branding     OrgBranding `json:"branding" datastore:"branding"`
	Licensed     bool        `json:"licensed" datastore:"licensed"` //Track onprem license
	OldOrg       bool        `json:"old_org" datastore:"old_org"`   // This is true for org older then 30 days
}

func GetEsConfig(defaultCreds bool) *opensearchapi.Client {
	esUrl := os.Getenv("SHUFFLE_OPENSEARCH_URL")
	if len(esUrl) == 0 {
		esUrl = "https://shuffle-opensearch:9200"
	}

	username := os.Getenv("SHUFFLE_OPENSEARCH_USERNAME")
	if len(username) == 0 {
		username = "admin"
	}

	password := os.Getenv("SHUFFLE_OPENSEARCH_PASSWORD")
	if len(password) == 0 {
		// New password that is set by default.
		// Security Audit points to changing this during onboarding.
		password = "StrongShufflePassword321!"
	}

	if defaultCreds {
		log.Printf("[DEBUG] Using default credentials for Opensearch (previous versions)")

		username = "admin"
		password = "admin"
	}

	log.Printf("[DEBUG] Using custom opensearch url '%s'", esUrl)

	// https://github.com/elastic/go-opensearch/blob/f741c073f324c15d3d401d945ee05b0c410bd06d/opensearch.go#L98
	config := opensearch.Config{
		Addresses:     strings.Split(esUrl, ","),
		Username:      username,
		Password:      password,
		MaxRetries:    5,
		RetryOnStatus: []int{500, 502, 503, 504, 429, 403},
	}

	if len(os.Getenv("SHUFFLE_OPENSEARCH_APIKEY")) > 0 {
		config.Username = ""
		config.Password = ""
		if config.Header == nil {
			config.Header = make(http.Header)
		}

		config.Header["Authorization"] = []string{"ApiKey " + os.Getenv("SHUFFLE_OPENSEARCH_APIKEY")}

	}

	//APIKey:        os.Getenv("SHUFFLE_OPENSEARCH_APIKEY"),
	//CloudID:       os.Getenv("SHUFFLE_OPENSEARCH_CLOUDID"),

	//config.Transport.TLSClientConfig
	//transport := http.DefaultTransport.(*http.Transport).Clone()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 100
	transport.ResponseHeaderTimeout = time.Second * 10
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = false

	if len(os.Getenv("SHUFFLE_OPENSEARCH_PROXY")) > 0 {
		httpProxy := os.Getenv("SHUFFLE_OPENSEARCH_PROXY")

		url_i := url.URL{}
		url_proxy, err := url_i.Parse(httpProxy)
		if err == nil {
			log.Printf("[DEBUG] Setting Opensearch proxy to %s", httpProxy)
			transport.Proxy = http.ProxyURL(url_proxy)
		} else {
			log.Printf("[ERROR] Failed setting proxy for %s", httpProxy)
		}
	}

	skipSSLVerify := false
	if strings.ToLower(os.Getenv("SHUFFLE_OPENSEARCH_SKIPSSL_VERIFY")) == "true" {
		//log.Printf("[DEBUG] SKIPPING SSL verification with Opensearch")
		skipSSLVerify = true
	}

	transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS11,
		InsecureSkipVerify: skipSSLVerify,
	}

	//https://github.com/elastic/go-opensearch/blob/master/_examples/security/opensearch-cluster.yml
	certificateLocation := os.Getenv("SHUFFLE_OPENSEARCH_CERTIFICATE_FILE")
	if len(certificateLocation) > 0 {
		cert, err := ioutil.ReadFile(certificateLocation)
		if err != nil {
			log.Fatalf("[WARNING] Failed configuring certificates: %s not found", err)
		} else {
			config.CACert = cert

			//if transport.TLSClientConfig.RootCAs, err = x509.SystemCertPool(); err != nil {
			//	log.Fatalf("[ERROR] Problem adding system CA: %s", err)
			//}

			//// --> Add the custom certificate authority
			//if ok := transport.TLSClientConfig.RootCAs.AppendCertsFromPEM(cert); !ok {
			//	log.Fatalf("[ERROR] Problem adding CA from file %q", *cert)
			//}
		}

		log.Printf("[INFO] Added certificate %s elastic client.", certificateLocation)
	}

	config.Transport = transport
	es, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: config,
		},
	)

	if err != nil {
		log.Fatalf("[ERROR] Database client for ELASTICSEARCH error during init (fatal): %s", err)
	}

	return es
}

type AppAuthSearchWrapper struct {
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
	Shards   struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	Hits struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore float64 `json:"max_score"`
		Hits     []struct {
			Index  string                   `json:"_index"`
			Type   string                   `json:"_type"`
			ID     string                   `json:"_id"`
			Score  float64                  `json:"_score"`
			Source AppAuthenticationStorage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type OrgWrapper struct {
	Index       string `json:"_index"`
	Type        string `json:"_type"`
	ID          string `json:"_id"`
	Version     int    `json:"_version"`
	SeqNo       int    `json:"_seq_no"`
	PrimaryTerm int    `json:"_primary_term"`
	Found       bool   `json:"found"`
	Source      Org    `json:"_source"`
}

type OrgMini struct {
	Name      string     `json:"name" datastore:"name"`
	Id        string     `json:"id" datastore:"id"`
	//Users     []UserMini `json:"users" datastore:"users"`
	Role      string     `json:"role" datastore:"role"`
	ChildOrgs []OrgMini  `json:"child_orgs" datastore:"child_orgs"`
	RegionUrl string     `json:"region_url" datastore:"region_url"`
	IsPartner bool       `json:"is_partner" datastore:"is_partner"`

	// Branding related
	Image      string      `json:"image" datastore:"image,noindex"`
	CreatorOrg string      `json:"creator_org" datastore:"creator_org"`
	//Branding   OrgBranding `json:"branding" datastore:"branding"`
}
