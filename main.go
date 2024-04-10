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

type ExitCode int

const (
	NoError ExitCode = iota
	BadFlag
	SendFailed
	ReadFileFailed
	WriteFileFailed
)

func (e ExitCode) Int() int {
	return int(e)
}

type Options struct {
	url      string
	method   string
	data     string
	output   string
	remote   string
	transfer string
	headers  []string
	accept   []string
	verbose  bool
	fail     bool
	include  bool
	version  bool
	silent   bool
	user     string
	agent    string
}

var Version = "1.0.0a"
var options = Options{}

func handleFlags() {
	args := os.Args[1:]
	options.url = args[len(args)-1]

	flag.StringVarP(&options.data, "data", "d", "", "HTTP POST data")
	flag.BoolVarP(&options.fail, "fail", "f", false, "Fail silently (no output at all) on HTTP errors")
	var help = flag.BoolP("help", "h", false, "help")
	flag.BoolVarP(&options.include, "include", "i", false, "Include protocol response headers in the output")   // TODO
	flag.StringVarP(&options.output, "output", "o", "", "Write to file instead of stdout")                      // TODO
	flag.StringVarP(&options.remote, "remote-name", "O", "", "Write output to a file named as the remote file") // TODO
	flag.BoolVarP(&options.silent, "silent", "s", false, "Silent mode")
	flag.StringVarP(&options.transfer, "upload-file", "T", "", "Transfer local FILE to destination")
	flag.StringVarP(&options.user, "user", "u", "", "Server user and password <user:password>") // TODO
	flag.StringVarP(&options.agent, "user-agent", "A", "go-gurl", "Send User-Agent <name> to server")
	flag.StringVarP(&options.method, "request", "X", "GET", "HTTP request method: GET, POST, PUT, DELETE")
	flag.StringSliceVarP(&options.headers, "header", "H", []string{"Content-Type: application/json"}, "Pass custom headers to server <key: value>")
	flag.StringSliceVarP(&options.accept, "accept", "a", []string{"*/*"}, "Accept headers (comma separated)")
	flag.BoolVarP(&options.verbose, "verbose", "v", false, "verbose mode")
	flag.Usage = usage
	flag.Parse()

	if *help {
		usage()
		os.Exit(NoError.Int())
	}

	if options.url == "" {
		fmt.Println("URL is mandatory")
		flag.Usage()
		os.Exit(BadFlag.Int())
	}

	if options.data != "" && !flag.CommandLine.Changed("request") {
		options.method = fasthttp.MethodPost
	}

	switch options.method {
	case fasthttp.MethodGet:
	case fasthttp.MethodPost:
	case fasthttp.MethodPut:
	case fasthttp.MethodDelete:
	case fasthttp.MethodPatch:
		fallthrough
	case fasthttp.MethodOptions:
		fallthrough
	case fasthttp.MethodHead:
		fallthrough
	case fasthttp.MethodConnect:
		fallthrough
	case fasthttp.MethodTrace:
		fmt.Println("HTTP method not currently supported: ", options.method)
	default:
		fmt.Println("Invalid HTTP method: ", options.method)
		os.Exit(BadFlag.Int())
	}
}

func usage() {
	fmt.Printf("go-gurl version %s\n", Version)
	fmt.Println("Usage: go-gurl [options...] <url>")
	flag.PrintDefaults()
}

func fetchURL() (req fasthttp.Request, rsp fasthttp.Response, e error) {
	if _, ue := url.Parse(options.url); ue != nil {
		return fasthttp.Request{}, fasthttp.Response{}, fmt.Errorf("Error parsing url %s: %s\n", options.url, ue)
	}

	req.SetRequestURI(options.url)
	req.SetBodyString(options.data)

	for _, h := range options.headers {
		hkv := strings.SplitN(h, ": ", 2)
		if len(hkv) < 2 {
			fmt.Printf("Warning: invalid header: %s\n", h)
			continue
		}
		req.Header.Add(hkv[0], hkv[1])
	}
	req.Header.Add("User-Agent", options.agent)
	for _, a := range options.accept {
		req.Header.Add("Accept", a)
	}

	req.Header.SetMethod(options.method)

	if e = fasthttp.Do(&req, &rsp); e != nil {
		return fasthttp.Request{}, fasthttp.Response{}, fmt.Errorf("Error making request: %s\n", e)
	}

	return
}

func handleTransfer() {
	if options.transfer != "" {
		if options.data != "" {
			fmt.Println("Options transfer and data are not compatible. Please choose one or the other, but not both")
			os.Exit(BadFlag.Int())
		}
		if b, e := os.ReadFile(options.transfer); e != nil {
			fmt.Printf("Unable to read file at %s: %s\n", options.transfer, e.Error())
			os.Exit(ReadFileFailed.Int())
		} else if len(b) > 0 {
			options.data = string(b)
		} else {
			fmt.Printf("Warning, transfer file %s appears to be empty\n", options.transfer)
		}
	}
}

func handleRequestResponse() {
	if req, rsp, e := fetchURL(); e == nil {
		httpOk := rsp.StatusCode() >= 200 && rsp.StatusCode() < 300
		if options.output != "" && !(options.fail && !httpOk) {
			if err := os.WriteFile(options.output, rsp.Body(), 0666); err != nil {
				fmt.Printf("Error writing to file %s: %s\n", options.output, err.Error())
				os.Exit(WriteFileFailed.Int())
			}
		}

		if options.output == "" && !options.silent && !(options.fail && !httpOk) {
			if options.verbose {
				fmt.Printf("Wrote: %d bytes\n", len([]byte(options.data)))
				fmt.Printf("Read : %d bytes\n", len(rsp.Body()))
				fmt.Printf("Request Headers:\n%s\n", bytes.Trim(req.Header.Header(), "\n"))
				fmt.Printf("Request Body:\n%s\n\n", req.Body())
				fmt.Printf("Response Headers:\n%s\n", bytes.Trim(rsp.Header.Header(), "\n"))
			}
			fmt.Printf("Response:\n%s\n", bytes.Trim(rsp.Body(), "\n"))
		}
	} else if !options.silent {
		fmt.Printf("Failed to send request: %s\n", fmt.Errorf("%w", e))
		os.Exit(SendFailed.Int())
	}
}

func main() {
	handleFlags()
	handleTransfer()
	handleRequestResponse()
}
