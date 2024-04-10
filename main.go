package main

import (
	"bytes"
	"fmt"
	flag "github.com/spf13/pflag"
	"github.com/valyala/fasthttp"
	"net/url"
	"os"
	"strings"
)

type Options struct {
	url      string
	method   string
	data     string
	output   string
	remote   string
	transfer string
	headers  []string
	accept   []string
	keepOpen bool
	verbose  bool
	fail     bool
	include  bool
	version  bool
	silent   bool
	user     string
	agent    string
}

var Version string = "1.0.0a"
var options Options = Options{}

func handleFlags() {
	args := os.Args[1:]
	options.url = args[len(args)-1]

	flag.StringVarP(&options.data, "data", "d", "", "HTTP POST data")
	flag.BoolVarP(&options.fail, "fail", "f", false, "Fail silently (no output at all) on HTTP errors")
	var help *bool = flag.BoolP("help", "h", false, "help")
	flag.BoolVarP(&options.include, "include", "i", false, "Include protocol response headers in the output")
	flag.StringVarP(&options.output, "output", "o", "", "Write to file instead of stdout")
	flag.StringVarP(&options.remote, "remote-name", "O", "", "Write output to a file named as the remote file")
	flag.BoolVarP(&options.silent, "silent", "s", false, "Silent mode")
	flag.StringVarP(&options.transfer, "upload-file", "T", "", "Transfer local FILE to destination")
	flag.StringVarP(&options.user, "user", "u", "", "Server user and password <user:password>")
	flag.StringVarP(&options.agent, "user-agent", "A", "", "Send User-Agent <name> to server")
	flag.StringVarP(&options.method, "request", "X", "GET", "HTTP request method: GET, POST, PUT, DELETE")
	flag.StringSliceVarP(&options.headers, "header", "H", []string{"Content-Type: application/json"}, "Pass custom headers to server <key: value>")
	flag.StringSliceVarP(&options.accept, "accept", "a", []string{"Accept: */*"}, "accept headers")
	flag.BoolVarP(&options.keepOpen, "keep-open", "", false, "keep connection open")
	flag.BoolVarP(&options.verbose, "verbose", "v", false, "verbose mode")
	flag.Usage = usage
	flag.Parse()

	if *help {
		usage()
		return
	}

	if options.url == "" {
		fmt.Println("URL is mandatory")
		flag.Usage()
	}

	if options.data != "" && options.method == "GET" {
		options.method = "POST"
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "go-gurl version %s\n", Version)
	fmt.Fprintln(os.Stderr, "Usage:")

	flag.PrintDefaults()
	os.Exit(0)
}

func fetchURL() (req fasthttp.Request, rsp fasthttp.Response, e error) {
	if _, ue := url.Parse(options.url); ue != nil {
		return fasthttp.Request{}, fasthttp.Response{}, fmt.Errorf("Error parsing url %s: %s\n", options.url, ue)
	}

	req.SetRequestURI(options.url)
	req.SetBodyString(options.data)

	for _, h := range options.headers {
		hkv := strings.SplitN(h, ": ", 1)
		if len(hkv) != 2 {
			// bad header
			continue
		}
		req.Header.Add(hkv[0], hkv[1])
	}
	req.Header.SetMethod(options.method)

	if e = fasthttp.Do(&req, &rsp); e != nil {
		return fasthttp.Request{}, fasthttp.Response{}, fmt.Errorf("Error making request: %s\n", e)
	}

	return
}

func main() {
	handleFlags()
	if req, rsp, e := fetchURL(); e == nil {
		if options.verbose {
			fmt.Printf("Wrote: %d bytes\n", len([]byte(options.data)))
			fmt.Printf("Read : %d bytes\n", len(rsp.Body()))
			fmt.Printf("Request Headers:\n%s\n", bytes.Trim(req.Header.Header(), "\n"))
			fmt.Printf("Request Body:\n%s\n\n", req.Body())
			fmt.Printf("Response Headers:\n%s\n", bytes.Trim(rsp.Header.Header(), "\n"))
		}
		fmt.Printf("Response:\n%s\n", bytes.Trim(rsp.Body(), "\n"))
	} else {
		fmt.Printf("Failed to send request: %s\n", fmt.Errorf("%w", e))
		os.Exit(2)
	}
}
