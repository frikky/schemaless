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

	"github.com/patrickmn/go-cache"
	"google.golang.org/appengine/memcache"
	gomemcache "github.com/bradfitz/gomemcache/memcache"
)

var mc = gomemcache.New(memcached)
var memcached = os.Getenv("SHUFFLE_MEMCACHED")
var requestCache = cache.New(60*time.Minute, 60*time.Minute)

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
		//log.Printf("[DEBUG] SKIPPING SSL verification with Opensearch")
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
	Authorization string `json:"authorization"`
	OrgId string `json:"orgId"`
	ExecutionId string `json:"execution_id"`
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

	//log.Printf("[DEBUG] Getting file from: %s", fileUrl)
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
func FindShuffleFile(name, category string, shuffleConfig ShuffleConfig) ([]byte, error) {
	if len(shuffleConfig.URL) < 1 {
		return []byte{}, errors.New("Shuffle URL not set")
	}



	// 1. Get the category 
	// 2. Find the file in the category output
	// 3. Read the file data
	// 4. Return it
	client := GetExternalClient(shuffleConfig.URL)
	categoryUrl := fmt.Sprintf("%s/api/v1/files/namespaces/%s?ids=true&filename=%s", shuffleConfig.URL, category, name)

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
		//return cacheData, nil
	} else {
		log.Printf("[INFO] Schemaless: Finding file %#v in category %#v from Shuffle backend", name, category)
		if len(shuffleConfig.ExecutionId) > 0 {
			categoryUrl += "&execution_id=" + shuffleConfig.ExecutionId
		}

		log.Printf("[DEBUG] Getting category WITHOUT cache: %s", categoryUrl)
		req, err := http.NewRequest(
			"GET", 
			categoryUrl,
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
			log.Printf("[ERROR] Schemaless (3): Error getting category %#v from Shuffle backend: %s", category, err)
			return []byte{}, err
		}


		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[ERROR] Schemaless (4): Error reading category %#v from Shuffle backend: %s", category, err)
			return []byte{}, err
		}

		go SetCache(ctx, cacheKey, body, 3)
		if resp.StatusCode != 200 {
			log.Printf("[ERROR] Schemaless: Bad status code (2) getting category %#v from Shuffle backend %#v: %s", category, categoryUrl, resp.Status)
			return []byte{}, errors.New(fmt.Sprintf("Bad status code: %s", resp.Status))
		}
	}

	// Unmarshal to Filestructure struct
	files := Filestructure{}
	err = json.Unmarshal(body, &files)
	if err != nil {
		log.Printf("[ERROR] Schemaless (5): Error unmarshalling category %#v from Shuffle backend: %s", category, err)
		return []byte{}, err
	}

	name = strings.TrimSpace(strings.ToLower(strings.Replace(name, " ", "_", -1)))
	if strings.HasSuffix(name, ".json") {
		name = name[:len(name)-5]
	}


	for _, file := range files.List {
		if file.Status != "active" {
			continue
		}

		filename := strings.TrimSpace(strings.ToLower(strings.Replace(file.Name, " ", "_", -1)))
		if strings.HasSuffix(filename, ".json") {
			filename = filename[:len(filename)-5]
		}

		//if strings.Contains(filename, name) {
		if filename != name { 
			continue
		}

		//log.Printf("\n\n[DEBUG] Found file %#v in category %#v. ID: %#v, Status: %#v\n\n", filename, category, file.Id, file.Status)

		downloadedFile, err := GetShuffleFileById(file.Id, shuffleConfig)
		if err != nil {
			log.Printf("[ERROR] Schemaless (6): Error getting file %#v from Shuffle backend: %s", name, err)
			return []byte{}, err
		}
		//log.Printf("\n\n[DEBUG] Got downloaded file data:\n%s\n\n", downloadedFile)

		return downloadedFile, nil
	}

	return []byte{}, errors.New(fmt.Sprintf("Failed to find translation file matching name '%s' in category '%s'", name, category)) 
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

