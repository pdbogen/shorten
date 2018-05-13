package main

import (
	"flag"
	"net/http"
	"fmt"
	"log"
	"strings"
	"time"
	"github.com/coreos/bbolt"
	"os"
	"encoding/json"
	"math/rand"
	"strconv"
)

func httpLog(status int, req *http.Request) {
	log.Printf(
		"%s - - %s %q %d -",
		req.RemoteAddr,
		time.Now().Format("[02/Jan/2006:15:04:05 -0700]"),
		req.Method+" "+req.RequestURI+" "+req.Proto,
		status,
	)
}

func withAuth(secret string, f func(rw http.ResponseWriter, req *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(rw http.ResponseWriter, req *http.Request) {
		auth := req.Header.Get("authorization")
		components := strings.SplitN(auth, " ", 2)

		if len(components) != 2 || strings.ToLower(components[0]) != "bearer" || components[1] != secret {
			httpLog(http.StatusUnauthorized, req)
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}

		f(rw, req)
	}
}

type Key struct {
	Url    string
	Expiry time.Time
}

func Serve(db *bolt.DB) func(rw http.ResponseWriter, req *http.Request) {
	return func(rw http.ResponseWriter, req *http.Request) {
		err := db.View(func(tx *bolt.Tx) error {
			keyBucket := tx.Bucket([]byte("keys"))
			if keyBucket == nil {
				httpLog(http.StatusNotFound, req)
				http.NotFound(rw, req)
				return nil
			}

			keyStr := strings.TrimLeft(req.URL.Path, "/")
			key := keyBucket.Get([]byte(keyStr))
			if key == nil {
				httpLog(http.StatusNotFound, req)
				http.NotFound(rw, req)
				return nil
			}

			keyObj := &Key{}
			if err := json.Unmarshal(key, keyObj); err != nil {
				log.Printf("corrupted key %q -> %q", keyStr, string(key))
				httpLog(http.StatusInternalServerError, req)
				http.Error(rw, "internal server error", http.StatusInternalServerError)
				return nil
			}

			if keyObj.Expiry.Before(time.Now()) {
				log.Printf("%s expired", keyStr)
				httpLog(http.StatusNotFound, req)
				http.NotFound(rw, req)
			}

			httpLog(http.StatusMovedPermanently, req)
			http.Redirect(rw, req, keyObj.Url, http.StatusMovedPermanently)
			return nil
		})
		if err != nil {
			log.Printf("error unhandled in db.View: %s", err)
			httpLog(http.StatusInternalServerError, req)
			http.Error(rw, "internal server error", http.StatusInternalServerError)
		}
	}
}

func Mint(db *bolt.DB) func(rw http.ResponseWriter, req *http.Request) {
	return func(rw http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			log.Printf("parsing form data: %s", err)
			httpLog(http.StatusBadRequest, req)
			http.Error(rw, "could not parse form data", http.StatusBadRequest)
			return
		}

		url := req.FormValue("url")
		if url == "" {
			log.Print("missing url in form data")
			httpLog(http.StatusBadRequest, req)
			http.Error(rw, "missing `url`", http.StatusBadRequest)
			return
		}

		key := []byte(strconv.FormatInt(rand.Int63(), 36))
		err := db.Update(func(tx *bolt.Tx) error {
			urlBucket, err := tx.CreateBucketIfNotExists([]byte("urls"))
			if err != nil {
				return fmt.Errorf("opening bucket `urls`: %s", err)
			}

			keyBucket, err := tx.CreateBucketIfNotExists([]byte("keys"))
			if err != nil {
				return fmt.Errorf("opening bucket `keys`: %s", err)
			}

			keyId := urlBucket.Get([]byte(url))
			if keyId != nil {
				key = keyId
			}

			keyObj := &Key{}
			keyBytes := keyBucket.Get(keyId)
			if keyBytes != nil {
				if err := json.Unmarshal(keyBytes, keyObj); err != nil {
					keyObj = &Key{}
				}
			}
			keyObj.Url = url
			keyObj.Expiry = time.Now().Add(time.Hour * 24 * 30)
			keyBytes, err = json.Marshal(keyObj)

			if err != nil {
				return fmt.Errorf("marshaling %v to JSON: %s", keyObj, err)
			}

			if err := urlBucket.Put([]byte(url), key); err != nil {
				return fmt.Errorf("saving to urls %q -> %q: %s", url, string(key), err)
			}
			if err := keyBucket.Put(key, keyBytes); err != nil {
				return fmt.Errorf("saving to keys %q -> %q: %s", string(key), string(keyBytes), err)
			}

			return nil
		})
		if err != nil {
			log.Printf("error updating DB: %s", err)
			httpLog(http.StatusInternalServerError, req)
			http.Error(rw, "internal server error", http.StatusInternalServerError)
		}

		rw.WriteHeader(http.StatusOK)
		fmt.Fprint(rw, string(key))
		return
	}
}

func Expirer(db *bolt.DB) {
	for {
		err := db.Update(func(tx *bolt.Tx) error {
			keyBucket := tx.Bucket([]byte("keys"))
			if keyBucket == nil {
				return nil
			}

			keyDeletes := [][]byte{}
			urlDeletes := [][]byte{}
			keyBucket.ForEach(func(k, v []byte) error {
				keyObj := &Key{}
				err := json.Unmarshal(v, keyObj)
				if err != nil || keyObj.Expiry.Before(time.Now()) {
					if err != nil {
						log.Printf("bad json %q: %s", k, err)
					}
					keyDeletes = append(keyDeletes, k)
					urlDeletes = append(urlDeletes, []byte(keyObj.Url))
				}
				return nil
			})
			for _, k := range keyDeletes {
				keyBucket.Delete(k)
			}

			if len(keyDeletes) > 0 {
				log.Printf("expired %d keys", len(keyDeletes))
			}

			urlBucket := tx.Bucket([]byte("urls"))
			if urlBucket != nil {
				for _, u := range urlDeletes {
					urlBucket.Delete(u)
				}
			}

			return nil
		})
		if err != nil {
			log.Printf("consuming error during expirer: %s", err)
		}
		time.Sleep(time.Minute)
	}
}

func RandHex(bytes uint) string {
	data := make([]byte, bytes)
	n, err := rand.Read(data)
	if err != nil {
		panic(fmt.Sprintf("could not get random bytes: %s", err))
	}
	if uint(n) != bytes {
		panic("could not get enough random bytes")
	}

	return fmt.Sprintf("%02x", data)
}

func main() {
	rand.Seed(time.Now().UnixNano())

	secret := flag.String("secret", "", "the shared secret used to authenticate creation requests; randomly generated each run if left off.")
	port := flag.Int("port", 80, "port to listen on")
	db := flag.String("db", "shorten.db", "path to the boltdb")
	flag.Parse()

	if *secret == "" {
		*secret = RandHex(16)
		log.Printf("random key for this session is: %q", *secret)
	}

	dbObj, err := bolt.Open(*db, os.FileMode(0660), nil)
	if err != nil {
		log.Fatalf("opening db %q: %s", *db, err)
	}

	http.HandleFunc("/", Serve(dbObj))
	http.HandleFunc("/mint", withAuth(*secret, Mint(dbObj)))

	go Expirer(dbObj)

	log.Printf("Listening on :%d", *port)
	log.Print(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
