# Project Title

_go-gurl_, curl alternative, written in go-lang

## Description

An in-depth paragraph about your project and overview of use.

## Getting Started

### Dependencies

No external dependencies

### Executing program

```shell
go-gurl version 1.0.0a
Usage: go-gurl [options...] <url>
  -a, --accept strings       Accept headers (comma separated) (default [*/*])
  -d, --data string          HTTP POST data
  -f, --fail                 Fail silently (no output at all) on HTTP errors
  -H, --header strings       Pass custom headers to server <key: value> (default [Content-Type: application/json])
  -h, --help                 help
  -i, --include              Include protocol response headers in the output
      --keep-open            keep connection open
  -o, --output string        Write to file instead of stdout
  -O, --remote-name string   Write output to a file named as the remote file
  -X, --request string       HTTP request method: GET, POST, PUT, DELETE (default "GET")
  -s, --silent               Silent mode
  -T, --upload-file string   Transfer local FILE to destination
  -u, --user string          Server user and password <user:password>
  -A, --user-agent string    Send User-Agent <name> to server (default "go-gurl")
  -v, --verbose              verbose mode
```

## Help

```shell
go-curl -h
```

## Authors

Contributors names and contact info

Jonathan Machen  

## Version History

* 1.0.0a - alpha version, not yet feature complete

## License

This project is licensed under the MIT License - see the LICENSE.md file for details
