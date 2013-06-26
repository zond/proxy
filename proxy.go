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
	"strings"
)

type redirect struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
}

var redirects = make(map[string]redirect)

func createRedirectedUrl(urlIn *url.URL) (urlOut *url.URL, err error) {
	redirect, found := redirects[urlIn.Host]
	if !found {
		err = fmt.Errorf("Proxy does not know where to forward requests for %v", urlIn.Host)
		return
	}
	redirectUrl := redirect.target
	buf := bytes.NewBufferString(redirectUrl.Scheme)
	fmt.Fprint(buf, "://")
	if urlIn.User != nil {
		fmt.Fprintf(buf, "%v@", redirectUrl.User.String())
	}
	fmt.Fprintf(buf, "://%v", redirectUrl.Host)
	if redirectUrl.Path != "" {
		fmt.Fprintf(buf, "/%v", redirectUrl.Path)
	}
	fmt.Fprintf(buf, "/%v", urlIn.Path)
	if urlIn.RawQuery != "" {
		fmt.Fprintf(buf, "?%v", urlIn.RawQuery)
	}
	if urlIn.Fragment != "" {
		fmt.Fprintf(buf, "#%v", urlIn.Fragment)
	}
	urlOut, err = url.Parse(string(buf.Bytes()))
	return
}

func handleWebsocket(cIn *websocket.Conn) {
	configIn := cIn.Config()
	urlOut, err := createRedirectedUrl(configIn.Location)
	if err != nil {
		log.Println(err)
		return
	}
	configOut := &websocket.Config{
		Location:  urlOut,
		Origin:    configIn.Origin,
		Protocol:  configIn.Protocol,
		Version:   configIn.Version,
		TlsConfig: configIn.TlsConfig,
		Header:    configIn.Header,
	}
	bits := strings.Split(configIn.Header.Get("X-Forwarded-For"), ",")
	bits = append(bits, cIn.RemoteAddr().String())
	configOut.Header.Set("X-Forwarded-For", strings.Join(bits, ","))
	cOut, err := websocket.DialConfig(configOut)
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
		var b []byte
		for err := websocket.Message.Receive(cIn, &b); err == nil; err = websocket.Message.Receive(cIn, &b) {
			err = websocket.Message.Send(cOut, b)
		}
		log.Println(err)
	}()
	var b []byte
	for err = websocket.Message.Receive(cOut, &b); err == nil; err = websocket.Message.Receive(cOut, &b) {
		err = websocket.Message.Send(cIn, b)
	}
	log.Println(err)
}

func handle(w http.ResponseWriter, r *http.Request) {
	if strings.ToLower(r.Header.Get("Connection")) == "upgrade" && strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		websocket.Handler(handleWebsocket).ServeHTTP(w, r)
	} else {
		redirect, found := redirects[r.URL.Host]
		if !found {
			log.Println("Proxy does not know where to forward requests for %v", r.URL.Host)
			return
		}
		redirect.proxy.ServeHTTP(w, r)
	}
}

func main() {
	port := flag.Int("port", 80, "What port the proxy listens to")
	host := flag.String("host", "0.0.0.0", "What host the proxy listens to")
	flag.Usage = func() {
		fmt.Printf("Usage: %v FLAGS HOST0 TARGET0 ... HOSTn TARGETn\n", os.Args[0])
		fmt.Println("FLAGS:")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	for i := 1; i < len(args); i++ {
		hostname := args[i]
		i++
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
