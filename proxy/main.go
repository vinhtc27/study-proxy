package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

type Server struct {
	Url     *url.URL
	Alive   bool
	Reverse *httputil.ReverseProxy
}

func NewServer(s string) *Server {
	url, err := url.Parse(s)
	if err != nil {
		log.Fatal(err)
	}

	transport := &http.Transport{
		MaxIdleConns:          100,              // Adjust based on expected load.
		MaxIdleConnsPerHost:   10,               // Limit idle connections per host.
		MaxConnsPerHost:       0,                // No limit on the total connections per host.
		ResponseHeaderTimeout: 2 * time.Second,  // Adjust based on desired response time.
		ExpectContinueTimeout: 1 * time.Second,  // Adjust based on desired behavior.
		IdleConnTimeout:       30 * time.Second, // Adjust based on desired connection reuse.
		Dial: (&net.Dialer{
			Timeout:   1 * time.Second,  // Adjust connection timeout as needed.
			KeepAlive: 30 * time.Second, // Adjust keep-alive time as needed.
		}).Dial,
	}

	err = http2.ConfigureTransport(transport)
	if err != nil {
		log.Fatal(err)
	}

	reverse := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = url.Scheme
			req.URL.Host = url.Host
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, fmt.Sprintf("Origin server error %s", err), http.StatusInternalServerError)
		},
	}

	return &Server{
		Url:     url,
		Alive:   true,
		Reverse: reverse,
	}
}

type ServerPool struct {
	servers []*Server
	index   int64
}

func (s *ServerPool) AddServer(server *Server) {
	s.servers = append(s.servers, server)
}

func (s *ServerPool) GetServer() *Server {
	atomic.AddInt64(&s.index, 1)
	//TODO: check overflow s.index
	return s.servers[s.index%int64(len(s.servers))]
}

func main() {
	var serversArg string
	var websocketArg string
	flag.StringVar(&serversArg, "servers", "", "Load balanced servers, use commas to separate")
	flag.StringVar(&websocketArg, "websocket", "", "Whether to use websocket")
	flag.Parse()
	if len(serversArg) == 0 {
		log.Fatal("Missing servers parameter")
	}

	servers := strings.Split(serversArg, ",")
	serverPool := ServerPool{index: -1}
	for _, s := range servers {
		serverPool.AddServer(NewServer(s))
	}

	host := "127.0.0.1:9090"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server := serverPool.GetServer()
		if server != nil {
			server.Reverse.ServeHTTP(w, r)
			return
		}
		http.Error(w, "Origin server unavailable", http.StatusServiceUnavailable)
	})

	proxy := http.Server{
		Addr:              host,
		ReadTimeout:       1 * time.Second,
		WriteTimeout:      1 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       30 * time.Second,
		Handler:           handler,
		// Handler: token_bucket.RequestThrottler(handler, 100),
	}

	log.Printf("Proxy started at %s\n", host)
	if err := proxy.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
