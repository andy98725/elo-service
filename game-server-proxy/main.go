package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ProxyServer struct {
	httpPort int
	tcpPort  int
	logger   *logrus.Logger
	appName  string
}

func NewProxyServer(httpPort, tcpPort int, appName string) *ProxyServer {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logger.SetLevel(logrus.InfoLevel)

	return &ProxyServer{
		httpPort: httpPort,
		tcpPort:  tcpPort,
		logger:   logger,
		appName:  appName,
	}
}

func (p *ProxyServer) Start() error {
	// Start HTTP proxy
	go func() {
		if err := p.startHTTPProxy(); err != nil {
			p.logger.Fatalf("HTTP proxy failed: %v", err)
		}
	}()

	// Start TCP proxy
	go func() {
		if err := p.startTCPProxy(); err != nil {
			p.logger.Fatalf("TCP proxy failed: %v", err)
		}
	}()

	// Keep the main goroutine alive
	select {}
}

func (p *ProxyServer) startHTTPProxy() error {
	router := mux.NewRouter()

	// Handle all HTTP requests
	router.PathPrefix("/").HandlerFunc(p.handleHTTPRequest)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", p.httpPort),
		Handler: router,
	}

	p.logger.Infof("Starting HTTP proxy on port %d", p.httpPort)
	return server.ListenAndServe()
}

func (p *ProxyServer) startTCPProxy() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", p.tcpPort))
	if err != nil {
		return fmt.Errorf("failed to start TCP listener: %v", err)
	}
	defer listener.Close()

	p.logger.Infof("Starting TCP proxy on port %d", p.tcpPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			p.logger.Errorf("Failed to accept TCP connection: %v", err)
			continue
		}

		go p.handleTCPConnection(conn)
	}
}

func (p *ProxyServer) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// Extract machine ID and port from the request
	// Expected format: /{machineID}/{port}/...
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 {
		http.Error(w, "Invalid path format. Expected: /{machineID}/{port}/...", http.StatusBadRequest)
		return
	}

	machineID := pathParts[0]
	portStr := pathParts[1]

	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, "Invalid port number", http.StatusBadRequest)
		return
	}

	// Construct the target URL
	targetHost := fmt.Sprintf("%s.vm.%s.internal:%d", machineID, p.appName, port)
	targetURL := fmt.Sprintf("http://%s", targetHost)

	// Create reverse proxy
	target, err := url.Parse(targetURL)
	if err != nil {
		p.logger.Errorf("Failed to parse target URL %s: %v", targetURL, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Create proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Modify the request to remove the machine ID and port from the path
	r.URL.Path = "/" + strings.Join(pathParts[2:], "/")
	if r.URL.RawPath != "" {
		r.URL.RawPath = "/" + strings.Join(pathParts[2:], "/")
	}

	p.logger.Infof("Proxying HTTP request to %s: %s %s", targetHost, r.Method, r.URL.Path)

	// Set up error handling
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		p.logger.Errorf("Proxy error for %s: %v", targetHost, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)
}

func (p *ProxyServer) handleTCPConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Read the first few bytes to determine the target
	// We'll use a simple protocol: first 4 bytes for machine ID length, then machine ID, then 2 bytes for port
	buffer := make([]byte, 1024)
	n, err := clientConn.Read(buffer)
	if err != nil {
		p.logger.Errorf("Failed to read from client: %v", err)
		return
	}

	// For simplicity, we'll use a simple format: "machineID:port"
	// In a real implementation, you might want a more sophisticated protocol
	data := string(buffer[:n])
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		p.logger.Error("Invalid TCP connection format. Expected: machineID:port")
		return
	}

	machineID := parts[0]
	portStr := parts[1]

	port, err := strconv.Atoi(portStr)
	if err != nil {
		p.logger.Errorf("Invalid port number: %s", portStr)
		return
	}

	// Connect to the target machine
	targetHost := fmt.Sprintf("%s.vm.%s.internal:%d", machineID, p.appName, port)
	targetConn, err := net.DialTimeout("tcp", targetHost, 10*time.Second)
	if err != nil {
		p.logger.Errorf("Failed to connect to target %s: %v", targetHost, err)
		return
	}
	defer targetConn.Close()

	p.logger.Infof("Proxying TCP connection to %s", targetHost)

	// Create context for the connection
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start bidirectional copying
	go func() {
		io.Copy(targetConn, clientConn)
		cancel()
	}()

	go func() {
		io.Copy(clientConn, targetConn)
		cancel()
	}()

	// Wait for either direction to finish
	<-ctx.Done()
}

func main() {
	// Get configuration from environment variables
	httpPort := 8080
	if port := os.Getenv("HTTP_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			httpPort = p
		}
	}

	tcpPort := 8081
	if port := os.Getenv("TCP_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			tcpPort = p
		}
	}

	appName := os.Getenv("FLY_APP_NAME")
	if appName == "" {
		appName = "elo-service" // Default fallback
	}

	server := NewProxyServer(httpPort, tcpPort, appName)

	logrus.Infof("Starting game server proxy for app: %s", appName)
	logrus.Infof("HTTP proxy on port: %d", httpPort)
	logrus.Infof("TCP proxy on port: %d", tcpPort)

	if err := server.Start(); err != nil {
		logrus.Fatalf("Failed to start proxy server: %v", err)
	}
}
