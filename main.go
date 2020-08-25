package main

import (
	"encoding/json"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/julienschmidt/httprouter"
)

var logInfo = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.LUTC)
var logErr = log.New(os.Stderr, "Error: ", log.Ldate|log.Ltime|log.LUTC)
var cacheExt = ".json"

func main() {

	var listen = flag.String("listen", "localhost:8080", "api listen on host:port")
	var upstreamHost = flag.String("host", "", "upstream host")
	var cachePath = flag.String("cache-path", "data", "where to save cached json")
	var cacheWrite = flag.Bool("cache-write", true, "set to false to stop writing cache (readonly)")
	flag.Parse()
	if *upstreamHost == "" {
		logErr.Fatalln("host parameter is required")
		return
	}
	var CachePath string
	if filepath.IsAbs(*cachePath) {
		CachePath = filepath.Join(currentExecutablePath(), *cachePath)
	} else {
		CachePath = *cachePath
	}
	if _, err := os.Stat(CachePath); os.IsNotExist(err) {
		err := os.MkdirAll(CachePath, 0755)
		if err != nil {
			logErr.Panicln(err.Error())
		}
	}

	router := httprouter.New()
	router.GET("/*path", handleAll(*upstreamHost, CachePath, *cacheWrite))

	logInfo.Printf("listening on %s\n", *listen)
	logErr.Fatalln(http.ListenAndServe(*listen, router))
}
func handleAll(upstreamHost string, cachePath string, cacheWrite bool) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		upUrl := path.Join(upstreamHost, r.URL.Path)
		upUrl = strings.Replace(upUrl, "/", "//", 1)
		if r.URL.RawQuery != "" {
			upUrl += "?" + r.URL.RawQuery
		}

		var body io.Reader
		if r.Method != "GET" {
			body = r.Body
		}
		defer r.Body.Close()
		logInfo.Printf("[%s] %s\n", r.Method, upUrl)
		upReq, err := http.NewRequest(r.Method, upUrl, body)
		if err != nil {
			// failed to create new request
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
			return
		}
		// copy all headers to upstream request
		for k, vals := range r.Header {
			for _, v := range vals {
				upReq.Header.Add(k, v)
			}
		}
		client := &http.Client{}
		// call upstream server (host)
		upRes, err := client.Do(upReq)
		if err != nil {
			// first use cache file, if cache file doesn't exist, send origin error back.
			cacheFile := makeCacheFilePath(cachePath, r.URL.Path, false)
			cachedPayload, cacheLoadError := ioutil.ReadFile(cacheFile)
			cachedHeader, cachedHeaderError := ioutil.ReadFile(makeCacheFilePath(cachePath, r.URL.Path, true))
			if cacheLoadError != nil {
				// return none cached original error
				w.WriteHeader(500)
				w.Write([]byte(err.Error()))
				return
			}
			logInfo.Printf("return cached data for path %q\n", r.URL.Path)
			// write back cached data and header
			w.Header().Add("x-src", "from cached-proxy")
			if cachedHeaderError == nil {
				loadedHeader, err := httpHeaderFromString(cachedHeader)
				if err != nil {
					for k, vals := range loadedHeader {
						for _, v := range vals {
							w.Header().Add(k, v)
						}
					}
				}
			}
			w.Write(cachedPayload)
			return
		}
		defer upRes.Body.Close()

		payload, err := ioutil.ReadAll(upRes.Body)
		// write proxy response to both downstream and cache file
		w.Write(payload)

		if !cacheWrite {
			return // skip cache write
		}
		cacheFile := makeCacheFilePath(cachePath, r.URL.Path, false)
		logInfo.Printf("cache write: %s\n", cacheFile)
		cacheErr := ioutil.WriteFile(cacheFile, payload, 0644)
		if cacheErr != nil {
			logErr.Printf("caching IO failure %s\n", cacheErr.Error())
		}
		headerData := httpHeaderToString(upRes.Header)
		cacheErr = ioutil.WriteFile(makeCacheFilePath(cachePath, r.URL.Path, true), headerData, 0644)
		if cacheErr != nil {
			logErr.Printf("caching IO failure %s\n", cacheErr.Error())
		}
	}
}

func currentExecutablePath() string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		logErr.Panicln(err.Error())
	}
	return dir
}

// convert slash url path to legal file name, naif way.
func makeCacheFilePath(cachePath string, urlPath string, forHeader bool) string {
	urlPath = strings.TrimPrefix(urlPath, "/")
	escapePath := strings.ReplaceAll(urlPath, "/", "-")
	if forHeader {
		escapePath += ".header"
	} else {
		escapePath += cacheExt
	}
	return filepath.Join(cachePath, escapePath)
}

// convert header map of string: stringArray to json
func httpHeaderToString(header http.Header) []byte {
	b, _ := json.Marshal(header)
	return b
}

// parse header back to map of string : stringArray
func httpHeaderFromString(content []byte) (http.Header, error) {
	header := make(http.Header)
	err := json.Unmarshal(content, &header)
	return header, err
}
