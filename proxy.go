package main

import (
	"bytes"
	"code.google.com/p/go.net/websocket"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
)

type redirect struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
}

var redirects = make(map[string]redirect)

func createRedirectedUrl(urlIn *url.URL) (urlOut *url.URL) {
	redirect, found := redirects[urlIn.Host]
	if !found {
		panic(fmt.Errorf("Proxy does not know where to forward requests for %v", urlIn))
	}
	redirectUrl := redirect.target
	buf := bytes.NewBufferString(urlIn.Scheme)
	fmt.Fprintf(buf, "://")
	if urlIn.User != nil {
		fmt.Fprintf(buf, "%v@", urlIn.User.String())
	}
	fmt.Fprintf(buf, "%v", redirectUrl.Host)
	if redirectUrl.Path != "" {
		fmt.Fprintf(buf, "%v", redirectUrl.Path)
	}
	fmt.Fprintf(buf, "%v", urlIn.Path)
	if urlIn.Fragment != "" {
		fmt.Fprintf(buf, "#%v", urlIn.Fragment)
	}
	if urlIn.RawQuery != "" {
		fmt.Fprintf(buf, "?%v", urlIn.RawQuery)
	}
	var err error
	if urlOut, err = url.Parse(string(buf.Bytes())); err != nil {
		panic(err)
	}
	return
}

func handleWebsocket(cIn *websocket.Conn) {
	configIn := cIn.Config()
	urlOut := createRedirectedUrl(configIn.Location)
	configOut := *configIn
	configOut.Location = urlOut
	if configOut.Header == nil {
		configOut.Header = make(http.Header)
	}
	bits := strings.Split(configIn.Header.Get("X-Forwarded-For"), ",")
	bits = append(bits, cIn.RemoteAddr().String())
	configOut.Header.Set("X-Forwarded-For", strings.Join(bits, ","))
	cOut, err := websocket.DialConfig(&configOut)
	if err != nil {
		log.Println(err)
		return
	}
	defer func() {
		cOut.Close()
	}()
	go func() {
		defer func() {
			cIn.Close()
			cOut.Close()
		}()
		var s string
		for err := websocket.Message.Receive(cIn, &s); err == nil; err = websocket.Message.Receive(cIn, &s) {
			err = websocket.Message.Send(cOut, s)
		}
		log.Println(err)
	}()
	var s string
	for err = websocket.Message.Receive(cOut, &s); err == nil; err = websocket.Message.Receive(cOut, &s) {
		err = websocket.Message.Send(cIn, s)
	}
	log.Println(err)
}

func handle(w http.ResponseWriter, r *http.Request) {
	redirect, found := redirects[r.Host]
	if !found {
		w.WriteHeader(404)
		fmt.Fprintf(w, "Proxy does not know where to forward requests for %v\n", r.Host)
		return
	}
	if strings.ToLower(r.Header.Get("Connection")) == "upgrade" && strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		websocket.Handler(handleWebsocket).ServeHTTP(w, r)
	} else {
		redirect.proxy.ServeHTTP(w, r)
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	port := flag.Int("port", 80, "What port the proxy listens to")
	host := flag.String("host", "0.0.0.0", "What host the proxy listens to")
	flag.Usage = func() {
		fmt.Printf("Usage: %v FLAGS HOST0 TARGET0 ... HOSTn TARGETn\n", os.Args[0])
		fmt.Println("FLAGS:")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	for i := 0; i < len(args); i++ {
		hostname := args[i]
		i++
		if i == len(args) {
			flag.Usage()
			return
		}
		target, err := url.Parse(args[i])
		if err != nil {
			panic(err)
		}
		redirects[hostname] = redirect{
			target: target,
			proxy:  httputil.NewSingleHostReverseProxy(target),
		}
	}

	if len(redirects) == 0 {
		flag.Usage()
		return
	}

	if err := http.ListenAndServe(fmt.Sprintf("%v:%v", *host, *port), http.HandlerFunc(handle)); err != nil {
		log.Println(err)
	}
}
