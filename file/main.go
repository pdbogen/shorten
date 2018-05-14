package main

import (
	"os"
	"log"
	"regexp"
	"fmt"
	"flag"
	"net/http"
	"io/ioutil"
	"net/url"
	"sync"
)

var urlRegexp = regexp.MustCompile(`(https?://[^) \n\t}\]]+)`)

var cache = map[string]string{}
var cacheMu = &sync.RWMutex{}

func Shorten(key string, subject string) string {
	cacheMu.RLock()
	ret, ok := cache[subject]
	cacheMu.RUnlock()

	if ok {
		return ret
	}

	vals := url.Values{
		"url": []string{subject},
	}

	pbUrl := url.URL{
		Scheme:   "https",
		Host:     "p-b.us",
		Path:     "mint",
		RawQuery: vals.Encode(),
	}

	req, err := http.NewRequest("GET", pbUrl.String(), nil)
	if err != nil {
		panic("error creating request: " + err.Error())
	}

	req.Header.Add("authorization", "bearer "+key)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("error sending mint request: %s", err)
		return subject
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		log.Printf("error reading body from response: %s", err)
		return subject
	}

	if res.StatusCode/100 != 2 {
		log.Printf("non-2XX minting: %s", string(body))
		return subject
	}

	cacheMu.Lock()
	cache[subject] = fmt.Sprintf("https://p-b.us/%s", string(body))
	cacheMu.Unlock()

	return cache[subject]
}

func main() {
	wg := &sync.WaitGroup{}

	secret := flag.String("secret", "", "authorization secret on p-b.us")
	flag.Parse()

	if flag.NArg() > 1 {
		log.Fatalf("usage: %s [<file>]", os.Args[0])
	}

	file := "/dev/stdin"

	if flag.NArg() == 1 {
		file = flag.Arg(0)
	}

	f, err := os.Open(file)
	if err != nil {
		log.Fatalf("opening %q: %s", file, err)
	}
	defer f.Close()

	contentBytes, err := ioutil.ReadAll(f)
	if err != nil {
		log.Fatalf("reading from %q: %s", file, err)
	}

	content := string(contentBytes)

	indexList := urlRegexp.FindAllStringIndex(content, -1)
	// First pass: run all the Shorten in parallel
	for _, indices := range indexList {
		wg.Add(1)
		go func() {
			Shorten(*secret, content[indices[0]:indices[1]])
			wg.Done()
		}()
	}

	last := []int{0, 0}
	for _, indices := range indexList {
		fmt.Print(content[last[1]:indices[0]])
		fmt.Print(Shorten(*secret, content[indices[0]:indices[1]]))
		last = indices
	}
	fmt.Print(content[last[1]:])
}
