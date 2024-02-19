package schemaless

import (
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"fmt"
	"io/ioutil"


	"context"
	"github.com/patrickmn/go-cache"
	"google.golang.org/appengine/memcache"
	gomemcache "github.com/bradfitz/gomemcache/memcache"
)

var mc = gomemcache.New(memcached)
var memcached = os.Getenv("SHUFFLE_MEMCACHED")
var requestCache = cache.New(60*time.Minute, 60*time.Minute)

var maxCacheSize = 1020000

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
	OrgId string `json:"orgId"`
	Authorization string `json:"authorization"`
}

// Finds a file in shuffle in a specified category
func FindShuffleFile(name, category string, shuffleConfig ShuffleConfig) ([]byte, error) {
	if len(shuffleConfig.URL) < 1 {
		return []byte{}, errors.New("Shuffle URL not set")
	}

	log.Printf("\n\n\n[INFO] Schemaless: Finding file %#v in category %#v from Shuffle backend\n\n\n", name, category)

	// 1. Get the category 
	// 2. Find the file in the category output
	// 3. Read the file data
	// 4. Return it
	client := GetExternalClient(shuffleConfig.URL)
	categoryUrl := fmt.Sprintf("%s/api/v1/files/namespaces/%s?ids=true", shuffleConfig.URL, category)
	log.Printf("[DEBUG] Getting category from: %s", categoryUrl)
	req, err := http.NewRequest(
		"GET", 
		categoryUrl,
		nil,
	)

	if err != nil {
		return []byte{}, err
	}

	log.Printf("[DEBUG] Adding authorization header to request: %#v", shuffleConfig.Authorization)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", shuffleConfig.Authorization))
	if len(shuffleConfig.OrgId) > 0 {
		req.Header.Add("OrgId", shuffleConfig.OrgId)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error getting category %#v from Shuffle backend: %s", category, err)
		return []byte{}, err
	}

	if resp.StatusCode != 200 {
		log.Printf("[ERROR] Schemaless: Bad status code getting category %#v from Shuffle backend: %s", category, resp.Status)
		return []byte{}, errors.New(fmt.Sprintf("Bad status code: %s", resp.Status))
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] Schemaless: Error reading category %#v from Shuffle backend: %s", category, err)
		return []byte{}, err
	}

	log.Printf("\n\n[DEBUG] Parsing response body from: %#v\n\n", string(body))


	return []byte{}, nil
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
