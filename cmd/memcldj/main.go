package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
)

const (
	version = "1.0.1"
	backoff = 50 * time.Millisecond
)

type options struct {
	hostport string
	key      string
	retry    uint
	verbose  bool
}

func worker(queue chan []string, opts options, wg *sync.WaitGroup) {
	defer wg.Done()
	mc := memcache.New(opts.hostport)
	for batch := range queue {
		for _, line := range batch {
			dst := make(map[string]interface{})
			err := json.Unmarshal([]byte(line), &dst)
			if err != nil {
				log.Fatal(err)
			}
			if _, ok := dst[opts.key]; !ok {
				log.Fatalf("key not found: %s", opts.key)
			}
			val := dst[opts.key]

			var id string
			switch val.(type) {
			case string:
				id = val.(string)
			case int:
				id = fmt.Sprintf("%d", val)
			case float64:
				id = fmt.Sprintf("%0d", int(val.(float64)))
			default:
				log.Fatalf("unsupported id value type: %v is a %v", val, reflect.TypeOf(val))
			}

			var ok bool
			var i uint

			for i = 1; i <= opts.retry; i++ {
				err = mc.Set(&memcache.Item{Key: id, Value: []byte(line)})
				if err != nil {
					pause := 2 << i * backoff
					if opts.verbose {
						log.Printf("retry %d for %s in %s ...", i, id, pause)
					}
					time.Sleep(pause)
				} else {
					ok = true
					break
				}
			}
			if !ok {
				log.Fatal(err)
			}
		}
	}
}

func main() {

	hostport := flag.String("addr", "127.0.0.1:11211", "hostport of memcache")
	key := flag.String("key", "id", "key to use")
	retry := flag.Int("retry", 10, "retry set operation this many times")
	numWorker := flag.Int("w", runtime.NumCPU(), "number of workers")
	size := flag.Int("b", 10000, "batch size")
	verbose := flag.Bool("verbose", false, "be verbose")
	showVersion := flag.Bool("v", false, "prints current program version")

	flag.Parse()

	runtime.GOMAXPROCS(*numWorker)

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		log.Fatal("input file required")
	}

	file, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	opts := options{
		hostport: *hostport,
		key:      *key,
		retry:    uint(*retry),
		verbose:  *verbose,
	}

	reader := bufio.NewReader(file)

	queue := make(chan []string)
	var wg sync.WaitGroup

	for i := 0; i < *numWorker; i++ {
		wg.Add(1)
		go worker(queue, opts, &wg)
	}

	var batch []string
	var i int

	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		batch = append(batch, line)
		if i%*size == 0 {
			if *verbose {
				log.Printf("@%d", i)
			}
			b := make([]string, len(batch))
			copy(b, batch)
			queue <- b
			batch = batch[:0]
		}
		i++
	}
	b := make([]string, len(batch))
	copy(b, batch)
	queue <- b

	if *verbose {
		log.Printf("@%d", i)
	}

	close(queue)
	wg.Wait()
}
