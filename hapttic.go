package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
    "strconv"
	"os/exec"
	"path/filepath"
	"unicode/utf8"
	"strings"
	"gopkg.in/yaml.v3"
)

const (
	version             = "2.0.0"
	defaultShell        = "/bin/sh"
    defaultShellScript  = "./hapttic_request_handler.sh"
    defaultShellCommand = "#!/bin/sh\necho $1"
)

type Config struct {
	Addr     string
	Bind     string `yaml:"bind"`
	Port     int    `yaml:"port"`
	Error    bool   `yaml:"error"`
	Scripts  map[string]string `yaml:"scripts"`
}

// This is a subset of http.Request with the types changed so that we can marshall it.
type marshallableRequest struct {
	Method string
	URL    string
	Proto  string
	Host   string

	Header http.Header

	ContentLength int64
	Body          string
	Form          url.Values
	PostForm      url.Values
}

func init() {
	log.SetOutput(os.Stdout)
}

func isFlagSet(name string) bool {
	flagStatus := false

    flag.Visit(func(f *flag.Flag) {
        if f.Name == name {
            flagStatus = true
        }
    })

    return flagStatus
}

func (c *Config) readYaml(file string) *Config {

    yamlFile, err := ioutil.ReadFile(file)
    if err != nil {
        log.Printf("yamlFile.Get err   #%v ", err)
    }
	
    err = yaml.Unmarshal(yamlFile, &c)
    if err != nil {
        log.Fatalf("Unmarshal: %v", err)
    }

    return c
}

func createShellScript(shellScript string, shellCommand string) {
	f, err := os.Create(shellScript)

    if err != nil {
        log.Fatal(err)
    }

    defer f.Close()

    _, err2 := f.WriteString(shellCommand)

    if err2 != nil {
        log.Fatal(err2)
    }
}

// handleFuncWithScriptFileName constructs our handleFunc
func handleFuncWithScriptFileName(shellScript string, logError bool) func(s http.ResponseWriter, req *http.Request) {
	return func(res http.ResponseWriter, req *http.Request) {

		// This parses the request body
		bodyBuffer := new(bytes.Buffer)
		bodyBuffer.ReadFrom(req.Body)
		body := bodyBuffer.String()

		req.ParseForm()

		// Copy over all the information from the request we are interested in
		marshallableReq := marshallableRequest{
			Method: req.Method,
			URL:    req.URL.String(),
			Proto:  req.Proto,
			Host:   req.Host,

			Header: req.Header,

			ContentLength: req.ContentLength,
			Body:          body,
			Form:          req.Form,
			PostForm:      req.PostForm,
		}

		// Try to convert to JSON. This shouldn't fail
		requestJSON, err := json.Marshal(marshallableReq)
		if err != nil {
			log.Fatal(err)
		}

		log.Println("Executing " + shellScript)

		// Execute the request handling script
		out, err := exec.Command(defaultShell, shellScript, string(requestJSON)).Output()

		if err != nil {
			// If there was an error, we return a response with status code 500
			res.WriteHeader(http.StatusInternalServerError)
			io.WriteString(res, "500 Internal Server Error")

			if logError {
				io.WriteString(res, "\n" + string(out))
			}

			if logError {
				log.Println("\033[33;31m--- ERROR: ---\033[0m")
				log.Println("\033[33;31mParams:\033[0m")
				log.Println(string(requestJSON))
				log.Println("\033[33;31mScript output:\033[0m")
				log.Println(string(out))
				log.Println("\033[33;31m---- END: ----\033[0m")
			}
		} else {
			// Otherwise we return the output of our script
			res.Write(out)
		}
	}
}

func main() {
	// Parse command line args
	printVersion   := flag.Bool("version", false, "Print version and exit.")
	printUsage     := flag.Bool("help", false, "Print usage and exit")
	configFile     := flag.String("config", "", "The yaml config file with settings. e.g. config.yml")
	shellScript    := flag.String("script", defaultShellScript, "The script that is called to handle requests.")
	shellCommand   := flag.String("command", defaultShellCommand, "The shell command instead of file")

	bind           := flag.String("bind", "0.0.0.0", "The host to bind to, e.g. 0.0.0.0 or localhost.")
	port           := flag.Int("port", 8080, "The port to listen on., e.g. 8080")
	logError       := flag.Bool("error", false, "Log errors to stdout")
	
	flag.Parse()

	if *printVersion {
		fmt.Fprintf(os.Stderr, version+"\n")
		os.Exit(0)
	}

	if *printUsage {
		fmt.Fprintf(os.Stderr, "Usage of hapttic:\n")
		flag.PrintDefaults()
		os.Exit(0)
	}

	var config  = new(Config)

	if isFlagSet("config") {config = config.readYaml(*configFile)}

	if config.Scripts == nil {config.Scripts = make(map[string]string)}

	if isFlagSet("error")  {config.Error = *logError}
	if isFlagSet("bind")   || config.Bind == "" {config.Bind = *bind}
	if isFlagSet("port")   || config.Port == 0  {config.Port = *port}
	if isFlagSet("error")  && isFlagSet("command") {*shellCommand += " 2>&1 "}
	if isFlagSet("script") || isFlagSet("command") || len(config.Scripts) == 0 {config.Scripts["/"] = *shellScript}

	config.Addr  = config.Bind + ":" + strconv.Itoa(config.Port)
	
	for httpPath, shellScript := range config.Scripts {
		
		if !isFlagSet("config") && utf8.RuneCountInString(shellScript) == 0 {
			log.Println(fmt.Sprintf("Parameter 'script' is empty. Default handling script will be used: %s", defaultShellScript))
			shellScript = defaultShellScript
		}

		shellScript, err := filepath.Abs(shellScript)

		if err != nil {
			delete(config.Scripts, httpPath)
		}
		
		if !isFlagSet("config") && isFlagSet("command") {
			log.Println(fmt.Sprintf("Parameter 'command' has been specified. Creating script: %s with content:\n%s", shellScript, *shellCommand))
			createShellScript(shellScript, *shellCommand)
		}

		if _, err := os.Stat(shellScript); os.IsNotExist(err) {
			if !isFlagSet("config") {
				log.Println(fmt.Sprintf("The request handling script %s does not exist.", shellScript))
				log.Println(fmt.Sprintf("Creating handling script: %s with default content:\n%s", shellScript, defaultShellCommand))
				createShellScript(shellScript, defaultShellCommand)
			} else {
				log.Println(fmt.Sprintf("Shell script %s does not exist. Removing from handling.", shellScript))
				delete(config.Scripts, httpPath)
				continue
			}
		}
		
		if !strings.HasPrefix(httpPath, "/") {
			delete(config.Scripts, httpPath)
			httpPath = "/" + httpPath
		}

		config.Scripts[httpPath] = shellScript
	}

	if len(config.Scripts) == 0 {
		log.Fatal("There's no any script provided")
	}

	log.Println(fmt.Sprintf("Thanks for using hapttic v%s", version))
	log.Println(fmt.Sprintf("Listening on %s", config.Addr))
	
	for httpPath, shellScript := range config.Scripts {
		log.Println(fmt.Sprintf("Forwarding requests %s to %s", httpPath, shellScript))
		go http.HandleFunc(httpPath, handleFuncWithScriptFileName(shellScript, config.Error))
	}

	if config.Error {
		log.Println("Logging errors to stderr")
	}

	log.Fatal(http.ListenAndServe(config.Addr, nil))
}
