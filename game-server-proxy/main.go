package main

import (
	"bufio"
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

	// Health check endpoint
	router.HandleFunc("/health", p.handleHealthCheck).Methods("GET")

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

// handleTCPConnection handles incoming TCP connections by proxying them to the appropriate game server.
// It reads the initial data from the client in a simple string format: "machineID:port"
// The connection is then proxied to {machineID}.vm.{appName}.internal:{port}.
// If any errors occur during connection setup or proxying, an error message is sent back to the client
// and the connection is closed.
func (p *ProxyServer) handleTCPConnection(clientConn net.Conn) {
	defer clientConn.Close()

	reader := bufio.NewReader(clientConn)
	data, err := reader.ReadString('\n')
	if err != nil {
		p.logger.Errorf("Failed to read from client: %v", err)
		clientConn.Write([]byte(fmt.Sprintf("Error reading from client: %v", err)))
		return
	}

	// Parse the machineID:port format
	parts := strings.Split(strings.TrimSpace(data), ":")
	if len(parts) < 2 {
		errMsg := "Invalid TCP connection format. Expected: machineID:port"
		p.logger.Error(errMsg)
		clientConn.Write([]byte(errMsg))
		return
	}

	machineID := parts[0]
	portStr := parts[1]

	port, err := strconv.Atoi(portStr)
	if err != nil {
		errMsg := fmt.Sprintf("Invalid port number: %s", portStr)
		p.logger.Error(errMsg)
		clientConn.Write([]byte(errMsg))
		return
	}

	// Connect to the target machine
	targetHost := fmt.Sprintf("%s.vm.%s.internal:%d", machineID, p.appName, port)
	targetConn, err := net.DialTimeout("tcp", targetHost, 10*time.Second)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to connect to target %s: %v", targetHost, err)
		p.logger.Error(errMsg)
		clientConn.Write([]byte(errMsg))
		return
	}
	defer targetConn.Close()

	p.logger.Infof("Proxying TCP connection to %s", targetHost)

	// Create context for the connection
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start bidirectional copying
	go func() {
		if _, err := io.Copy(targetConn, clientConn); err != nil {
			p.logger.Errorf("Error copying client->target: %v", err)
		}
		cancel()
	}()

	go func() {
		if _, err := io.Copy(clientConn, targetConn); err != nil {
			p.logger.Errorf("Error copying target->client: %v", err)
		}
		cancel()
	}()

	// Wait for either direction to finish
	<-ctx.Done()
}

func (p *ProxyServer) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy","service":"game-server-proxy"}`))
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
