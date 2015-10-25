// WebTree project

// you must use go get golang.org/x/exp
/*
You're free to copy and use this code

version 1.2 - Suggested to be used with Golang 1.5

To Enhance Program:
1. setable xpath with regex
2. package
3. save the files rather than file names
4. 
*/

package main

import (
	"bufio"
	"flag"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/net/html"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type nodeType struct {
	parent   *nodeType
	tag      string
	uri	     string
	dupped   bool
	file     bool
	last     bool
	children []*nodeType
}



var uri string
var user string
var pass string
var xpath string
var saveTree string
var mutex *sync.Mutex
var ptrShowFiles *bool
var ptrMakeTree *bool
var ptrShowDups *bool
var ptrShowElapsed *bool
var ptrShowStats *bool
var ptrFilesReadable *bool
var ptrPathStruct *bool
var root *nodeType
var visitedURIs map[string]bool
var fileCnt int
var dirCnt int
var dupCnt int
var unreadableCnt int
var treeCh chan *nodeType
var saverWG sync.WaitGroup

var releaseHTTPClientCh chan *http.Client
var requestHTTPClientCh chan (chan *http.Client)

const none = "(none)"

func main() {

	ptrShowDups = flag.Bool("showdups", false, "Show Duplicate URIs")
	ptrMakeTree = flag.Bool("replicate", false, "Replicate Dir Struct With Touched Files")
	ptrClientPoolSize := flag.Int("clientpool", 32, "Maximum HTTP(S) Clients To Use")
	ptrDurationHTTP := flag.Int("timeout-http", 30, "HTTP(S) Timeout For Clients To Use (secs)")
	ptrDurationKA := flag.Int("timeout-keepalive", 10, "KeepALive Duration For Clients To Use (secs)")
	ptrDurationTLS := flag.Int("timeout-tls", 60, "TLS Timeout For Clients To Use (secs)")
	ptrShowFiles = flag.Bool("showfiles", false, "Show Files - Only Show Dir Tree")
	ptrShowElapsed = flag.Bool("timer", false, "Show Elapsed Time")
	ptrShowStats = flag.Bool("showstats", false, "Show Count of Files, Directories, Duplicates")
	ptrFilesReadable = flag.Bool("testfiles", false, "Check That Files Are Able To Be Read")
	flag.StringVar(&user, "user", none, "User ID ... If HTTP(S) Authentication")
	flag.StringVar(&pass, "pass", none, "Password ... If HTTP(S) Authentication")
	flag.StringVar(&saveTree, "savetree", none, "Save Tree in filename")
//	flag.StringVar(&xpath, "xpath", "/html/body/table/tbody/tr/td/a", "XPath to find hrefs")
	flag.StringVar(&xpath, "xpath", "/html/body/ul/li/a", "XPath to find hrefs")

	flag.Parse()

	//	fmt.Println("SD:", *ptrShowDups)
	//	fmt.Println("SF:", *ptrShowFiles)
	// fmt.Println("SE:", *ptrShowElapsed)
	//	fmt.Println("User:", user)
	//	fmt.Println("Pass:", pass)
	//	fmt.Println("XPath:", xpath)
	//	fmt.Println("Tail:", flag.Args())
	//	uri = os.Args[1]
	//	fmt.Println("User:", user)
	//	fmt.Println("Pass:", pass)
	//	uri = flag.Args()[0]
	//	fmt.Println("URI:", uri)

	//	flag.PrintDefaults()
	//	flag.Usage()

	var startTime time.Time
	if *ptrShowElapsed {
		startTime = time.Now()
	}
	_, myname := filepath.Split(os.Args[0])
	var Usage = func() {
		fmt.Fprintf(os.Stderr, "\nUsage of %s:\n", myname)
		fmt.Fprintf(os.Stderr, "  %s flags URI [user [password]]\n\n", myname)
		fmt.Fprintf(os.Stderr, "Note:\n")
		fmt.Fprintf(os.Stderr, "  No user or password is required, but when user is set, a password can\n")
		fmt.Fprintf(os.Stderr, "  be provided as plain-text on the command line or via security prompt.\n")
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n\n")

	}
	//	Usage()
	if len(flag.Args()) > 0 {
		uri = flag.Arg(0)
		if len(flag.Args()) > 1 {
			if user == none {
				user = flag.Arg(1)
			} else {
				log.Panicf("Username supplied twice (as %s & %s).", user, flag.Arg(1))
			}
			if len(flag.Args()) == 3 {
				if pass == none {
					pass = flag.Arg(2)
				} else {
					Usage()
					log.Panicln("Password supplied twice.")
				}
			}
		}
	} else {
		Usage()	
		return	
	}
	if user != none && pass == none {
		if terminal.IsTerminal(0) && user != none {
			fmt.Printf("Password: ")
			passbyte, err := terminal.ReadPassword(0)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println()
			pass = string(passbyte[:])
			fmt.Println("Pass:", pass)
		} else {
			Usage()
			log.Panicln("Error: Password cannot be interactively supplied in a virtual terminal.")
		}
	}

	// make sure user gave URI or URL for base directory
	if uri[len(uri)-1] != '/' {
		uri += "/"
	}

	mutex = &sync.Mutex{}
	fileCnt = 0
	dirCnt = 0
	dupCnt = 0
	unreadableCnt = 0
	var amDoneWG sync.WaitGroup
	amDoneWG.Add(1)
	var lastWG sync.WaitGroup
	lastWG.Add(1)
	releaseHTTPClientCh = make(chan *http.Client)
	requestHTTPClientCh = make(chan (chan *http.Client))

	go manageClientPoolAndFactory(*ptrClientPoolSize, time.Duration(*ptrDurationHTTP), time.Duration(*ptrDurationKA), time.Duration(*ptrDurationTLS))

	if saveTree != none {
		saverWG.Add(1)
		treeCh = make(chan *nodeType)
		go saveTreeToFile(treeCh, saveTree)
	}
	visitedURIs = make(map[string]bool)

	parts, err := url.Parse(uri)
	if err != nil {
		log.Fatal(err)
	}
	pathElems := strings.Split(parts.Path, "/")
	temURI := parts.Scheme + "://" + parts.Host + "/"
	visitedURIs[temURI] = true
	//	fmt.Println(temURI, "- /")
	for _, dirElem := range pathElems {
		if dirElem != "" {
			temURI += dirElem + "/"
			visitedURIs[temURI] = true
			//			fmt.Println(temURI, "-", dirElem)
		}
	}

	visitedURIs[uri] = true
	var chopLoc int
	if strings.HasPrefix(uri, "https:\\") {
		chopLoc = 7				
	} else {
		chopLoc = 8				
	}			

	root = &nodeType{nil, uri, uri[chopLoc:], false, false, true, nil}
	amDoneWG.Done()
	go multipurposeTree(&amDoneWG, &lastWG, uri, root, "")
	lastWG.Wait()

	if saveTree != none {
		treeCh <- &nodeType{nil, none, uri, false, false, true, nil}
		saverWG.Wait()
		close(treeCh)
	}

	fmt.Println()
	if *ptrShowStats {

		if !*ptrFilesReadable {
			fmt.Printf("Statistics: %d files, %d directories, %d duplicated (dirs)\n", fileCnt, dirCnt, dupCnt)
			fmt.Println("Note: Per CLI flags, files not readable are not checked. To show, set the CLI testfiles flag.")
		} else {
			fmt.Printf("Statistics: %d files, %d directories, %d duplicated (dirs), %d unreadable (files) \n", fileCnt, dirCnt, dupCnt, unreadableCnt)
		}
		if !*ptrShowFiles {
			fmt.Println("Note: By CLI flags, files are not shown. To show, set the CLI showfiles flag.")
		}
		if !*ptrShowDups {
			fmt.Println("Note: Per CLI flags, duplicated directories are not shown. To show, set the CLI showdups flag.")
		}
	}

	if *ptrShowElapsed {
		fmt.Printf("Time Elapsed: %f secs\n", time.Since(startTime).Seconds())
	}
	//fmt.Println("Exited program")
}

//////////////////////////////////////////////////////////////////////////////
func saveTreeToFile(treeCh chan *nodeType, filename string) {

	// open filename
	f, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	for loop := true; loop; {
		select {
		case node := <-treeCh:
			if node.tag == none {
				loop = false
			} else {
				// building tree?
				if *ptrMakeTree {
					if node.file {
					//	fmt.Println("touch file : ", node.uri)
						temf, err := os.Create("./" + node.uri);
						if err != nil {
							log.Fatal(err)
						}
						defer temf.Close()
					} else {
					//	fmt.Println("Mkdir : ", node.uri)
						err := os.MkdirAll("./" + node.uri, 0777)
						if err != nil {
							log.Fatal(err)
						}
					}
				}
				
				fmt.Fprintln(w, *node)
			}
		}
	}
	w.Flush()
	saverWG.Done()
	// close filename
}

//////////////////////////////////////////////////////////////////////////////
func manageClientPoolAndFactory(poolSize int, durHTTP time.Duration, durKA time.Duration, durTLS time.Duration) {

	transpt := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   durHTTP * time.Second,
			KeepAlive: durKA * time.Second,
		}).Dial,
		TLSHandshakeTimeout: durTLS * time.Second,
	}
	requestQueue := make([]chan *http.Client, 0, 256)
	clientPool := make([]*http.Client, 0, poolSize)
	poolCnt := 0
	for loop := true; loop || len(requestQueue) > 0; {
		select {
		case availableClient := <-releaseHTTPClientCh:
			clientPool = append(clientPool[:], availableClient)
			//			fmt.Printf("Ret: pool = %d, queue = %d, avail = %d\n", poolCnt, len(requestQueue), len(clientPool))
		case requestCh := <-requestHTTPClientCh:
			if requestCh == nil {
				loop = false
			} else {
				requestQueue = append(requestQueue[:], requestCh)
				//				fmt.Printf("Ret: pool = %d, queue = %d, avail = %d\n", poolCnt, len(requestQueue), len(clientPool))
			}
		} // select

		for moreClients := true; len(requestQueue) > 0 && moreClients; {
			switch {
			case len(clientPool) > 0:
				requestQueue[0] <- clientPool[0]
				clientPool = append(clientPool[:0], clientPool[1:]...)
				requestQueue = append(requestQueue[:0], requestQueue[1:]...)
				//				fmt.Println("Pulling from pool")
			case poolCnt < poolSize:
				requestQueue[0] <- &http.Client{Transport: transpt}
				requestQueue = append(requestQueue[:0], requestQueue[1:]...)
				poolCnt++
				//				fmt.Printf("Created client #%d\n", poolCnt)
			default:
				moreClients = false
				//				fmt.Println("No clients or queue")
			}
		} // for / while

	}
	saverWG.Done()
}

//////////////////////////////////////////////////////////////////////////////

func multipurposeTree(canQuit *sync.WaitGroup, amDoneWG *sync.WaitGroup, link string, me *nodeType, indent string) {
	type childType struct {
		uri     string
		element string
		dupped  bool
		file    bool
	}
	items := []*childType{}
	readable := true
	//	fmt.Println("I am: ", me)
	if !me.file && !me.dupped {
		//		fmt.Println("Getting: ", link)
		getHTTPClientCh := make(chan *http.Client)
		requestHTTPClientCh <- getHTTPClientCh

		client := <-getHTTPClientCh

		req, err := http.NewRequest("GET", link, nil)
		if user != none {
			req.SetBasicAuth(user, pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		pageText, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		if resp.StatusCode != 200 {
			log.Fatalf("Getting %s failed HTTP GET response code %d\n", link, resp.StatusCode)
		}

		doc, err := html.Parse(strings.NewReader(string(pageText)))
		if err != nil {
			log.Fatal(err)
		}
		var nodeVisit func(string, *html.Node)
		nodeVisit = func(xpathNow string, n *html.Node) {
			if n.Type == html.ElementNode {
				xpathNow += "/" + n.Data
				if xpathNow == xpath {
					for _, atts := range n.Attr {
						if atts.Key == "href" {
							child := &childType{"", atts.Val, false, false}
							items = append(items, child)
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				// if xpath contains soughtxpath at start -- skip
				nodeVisit(xpathNow, c)
			}
		}
		nodeVisit("", doc)
		resp.Body.Close()
		releaseHTTPClientCh <- client
	} else {
		//		fmt.Println("Skipped Link: ", link)
	}
	if me.file && *ptrFilesReadable {

		getHTTPClientCh := make(chan *http.Client)
		requestHTTPClientCh <- getHTTPClientCh
		client := <-getHTTPClientCh

		req, err := http.NewRequest("HEAD", link, nil)
		if user != none {
			req.SetBasicAuth(user, pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		//if resp.StatusCode != 200 {
		//	fmt.Printf("%d - Testing = %s,\n%s\n", resp.StatusCode, link, resp.Body)
		//}
		if resp.StatusCode == 403 {
			readable = false
			unreadableCnt++
		}
		resp.Body.Close()
		releaseHTTPClientCh <- client
	}

	// mark dirs and files
	for i := 0; i < len(items); {
		if items[i].element[len(items[i].element)-1] == '/' {
			i++
			dirCnt++
		} else { // file
			if *ptrShowFiles {
				items[i].file = true
				i++
			} else {
				items = append(items[:i], items[i+1:]...)
			}
			fileCnt++
		}
	}

	for i := 0; i < len(items); {
		parts, _ := url.Parse(link)
		if items[i].element[0] == '/' {
			if len(items[i].element) > 1 {
				items[i].uri = parts.Scheme + "://" + parts.Host + items[i].element
			} else {
				items[i].uri = parts.Scheme + "://" + parts.Host + "/"
			}
		} else {
			items[i].uri = parts.Scheme + "://" + parts.Host + parts.Path + items[i].element
		}
		//		fmt.Println("parsed:", parts.Host, parts.Path, items[i].element, "\n    ", items[i].uri)
		mutex.Lock()
		_, defined := visitedURIs[items[i].uri]
		mutex.Unlock()
		if !defined {
			mutex.Lock()
			visitedURIs[items[i].uri] = true
			mutex.Unlock()
			//			fmt.Println(i, "new link = ", items[i].uri, " from ", items[i].element)
			i++
		} else { // duplicated
			if *ptrShowDups {
				items[i].dupped = true
				//				fmt.Println(i, "mark dup link = ", items[i].uri)
				i++
			} else {
				items = append(items[:i], items[i+1:]...)
				//				fmt.Println(i, "strip dup link = ", items[i].uri)
			}
			dupCnt++
		}
	}
	//fmt.Printf("Building Nodes\n")

	l := len(items)
	if l > 0 {
		ptrNotifyWG := make([]*sync.WaitGroup, l+1)
		notifyWG := make([]sync.WaitGroup, l)

		for i := 0; i < l; i++ {
			notifyWG[i].Add(1)
			ptrNotifyWG[i] = &notifyWG[i]
		}
		ptrNotifyWG[l] = amDoneWG
		me.children = make([]*nodeType, l)

		last := false
		for i, item := range items {
			if i == l-1 {
				last = true
			}
			var chopLoc int
			if strings.HasPrefix(item.uri, "https:\\") {
				chopLoc = 7				
			} else {
				chopLoc = 8				
			}			
			me.children[i] = &nodeType{me, item.element, item.uri[chopLoc:], item.dupped, item.file, last, nil}
			//			fmt.Println(i, item)
		}
		for i, item := range items {
			var childIndent string
			if me.last {
				childIndent = indent + "   "
			} else {
				childIndent = indent + "│  "
			}
			go multipurposeTree(ptrNotifyWG[i], ptrNotifyWG[i+1], item.uri, me.children[i], childIndent)
		}
		amDoneWG = ptrNotifyWG[0]
	}
	// to save files - see
	// http://stackoverflow.com/questions/1821811/how-to-read-write-from-to-file

	if saveTree != none {
		treeCh <- me
	}

	//	fmt.Printf("Waitin for quit from %p\n", canQuit)
	(*canQuit).Wait()

	if me.last {
		if len(indent) > 0 {
			indent += "└─"
		}
	} else {
		indent += "├─"
	}
	var visited string
	if me.dupped {
		visited = " (VISITED)"
	} else {
		visited = ""
	}
	var startBracket, stopBracket string
	if readable {
		startBracket, stopBracket = "", ""
	} else {
		startBracket = "**["
		stopBracket = "]**"
	}

	fmt.Printf("%s%s%s%s%s\n", indent, startBracket, me.tag, stopBracket, visited)

	//	fmt.Printf("Sending quit to %p\n", amDone)
	(*amDoneWG).Done()
	return

}
