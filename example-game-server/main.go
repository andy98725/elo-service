package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RequestBody struct {
	TokenID  string `json:"token_id"`
	WinnerID string `json:"winner_id"`
}

// expectedTokens / joinedTokens hold the per-match connect tokens the
// matchmaker hands the game server in argv. Each value is the credential
// a single player presents on /join (HTTP) or the first TCP line. Today
// it equals the player's ID; phase 2 swaps it for an opaque secret. The
// game server only needs to verify membership — the matchmaker is
// authoritative on which tokens are admissible for this match.
type GameServer struct {
	tokenID         string
	expectedTokens  map[string]bool
	joinedTokens    map[string]bool
	mutex           sync.RWMutex
	reportURL       string
	artifactURL     string
	shutdownChan    chan struct{}
}

// platformBase derives the platform's base URL from the report URL the
// container was configured with. Both /result/report and /match/artifact
// live on the same host, so we just strip the report path.
func platformBase(reportURL string) string {
	if i := strings.Index(reportURL, "/result/report"); i > 0 {
		return reportURL[:i]
	}
	return reportURL
}

func NewGameServer(tokenID string, connectTokens []string) *GameServer {
	reportURL := os.Getenv("WEBSITE_URL")
	if reportURL == "" {
		reportURL = "https://elo-service.fly.dev/result/report"
	}

	expectedTokens := make(map[string]bool)
	for _, t := range connectTokens {
		expectedTokens[t] = true
	}

	return &GameServer{
		tokenID:        tokenID,
		expectedTokens: expectedTokens,
		joinedTokens:   make(map[string]bool),
		reportURL:      reportURL,
		artifactURL:    platformBase(reportURL) + "/match/artifact",
		shutdownChan:   make(chan struct{}),
	}
}

func (gs *GameServer) addPlayer(connectToken string) bool {
	gs.mutex.Lock()
	defer gs.mutex.Unlock()

	if !gs.expectedTokens[connectToken] {
		return false // Token not expected
	}

	if gs.joinedTokens[connectToken] {
		return false // Already joined with this token
	}

	gs.joinedTokens[connectToken] = true
	log.Printf("Player joined (token=%s). Total: %d/%d", connectToken, len(gs.joinedTokens), len(gs.expectedTokens))

	// Check if all expected players have joined
	if len(gs.joinedTokens) >= len(gs.expectedTokens) {
		log.Println("All players have joined! Starting game...")
		go gs.reportResult()
		return true
	}

	return true
}

func (gs *GameServer) getJoinedTokens() []string {
	gs.mutex.RLock()
	defer gs.mutex.RUnlock()

	out := make([]string, 0, len(gs.joinedTokens))
	for t := range gs.joinedTokens {
		out = append(out, t)
	}
	return out
}

func (gs *GameServer) getExpectedTokens() []string {
	out := make([]string, 0, len(gs.expectedTokens))
	for t := range gs.expectedTokens {
		out = append(out, t)
	}
	return out
}

// uploadArtifact pushes a named binary artifact to the matchmaker. Used
// to demonstrate the platform's per-match artifact mechanism — game
// servers attach replays, preview images, etc. The match auth_code
// (same -token used for /result/report) authenticates the upload.
//
// Returns the HTTP status so the caller can log it; never fails the
// caller because artifact upload is best-effort.
func (gs *GameServer) uploadArtifact(name, contentType string, body []byte) int {
	url := fmt.Sprintf("%s?name=%s", gs.artifactURL, name)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("artifact %q: build request failed: %v", name, err)
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+gs.tokenID)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("artifact %q: request failed: %v", name, err)
		return 0
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	log.Printf("artifact %q upload: status=%d body=%s", name, resp.StatusCode, string(rb))
	return resp.StatusCode
}

func (gs *GameServer) reportResult() {
	// In phase 1, connect_token == player_id, so a joined token is also
	// a valid winner_id. Phase 2 will need an explicit (player_id,
	// connect_token) pairing in argv to keep this mapping correct.
	tokens := gs.getJoinedTokens()

	log.Printf("Simulating game with %d players: %v", len(tokens), tokens)
	time.Sleep(3 * time.Second)

	// Randomly select winner
	winnerID := tokens[rand.Intn(len(tokens))]
	log.Printf("Game finished! Winner: %s", winnerID)

	// Pre-result artifact upload — exercises the "during match" path
	// of the artifact API. Conventional name "preview" is what
	// platform-generic UIs render as a match-history thumbnail.
	gs.uploadArtifact("preview", "image/png", []byte("\x89PNG\r\nfake-preview-bytes"))

	// Prepare request body
	jsonData, err := json.Marshal(RequestBody{
		TokenID:  gs.tokenID,
		WinnerID: winnerID,
	})
	if err != nil {
		log.Printf("Failed to marshal JSON: %v", err)
		gs.shutdown()
		return
	}

	// Send result to elo service
	log.Printf("Sending result to %s", gs.reportURL)
	resp, err := http.Post(gs.reportURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		gs.shutdown()
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v", err)
		gs.shutdown()
		return
	}

	log.Printf("Result sent successfully. Status: %s, Body: %s", resp.Status, string(body))

	// Post-result artifact upload — exercises the cooldown contract:
	// after /result/report returns 2xx, the auth_code stays valid for
	// the configured cooldown window (default 5 min) so the game
	// server can finish post-match work like uploading a replay file.
	// A 200 here means cooldown is working; a 403 would mean the
	// platform tore the match down too eagerly.
	gs.uploadArtifact("replay", "application/octet-stream", []byte("post-result-replay-bytes"))

	// Shutdown the server after reporting results
	log.Println("Shutting down server...")
	gs.shutdown()
}

func (gs *GameServer) shutdown() {
	close(gs.shutdownChan)
}

// corsMiddleware wraps a handler to add CORS support and handle preflight requests.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next(w, r)
	}
}

// Health check handler
func (gs *GameServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gs.mutex.RLock()
	joinedCount := len(gs.joinedTokens)
	expectedCount := len(gs.expectedTokens)
	gs.mutex.RUnlock()
	expectedTokens := gs.getExpectedTokens()
	joinedTokens := gs.getJoinedTokens()

	response := map[string]interface{}{
		"status":          "healthy",
		"token_id":        gs.tokenID,
		"expected_tokens": expectedTokens,
		"joined_tokens":   joinedTokens,
		"player_count":    joinedCount,
		"expected_count":  expectedCount,
		"ready":           joinedCount >= expectedCount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HTTP handler for player registration
func (gs *GameServer) handleHTTPJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	connectToken := strings.TrimSpace(string(body))
	if connectToken == "" {
		http.Error(w, "Connect token is required", http.StatusBadRequest)
		return
	}

	if !gs.expectedTokens[connectToken] {
		http.Error(w, "Connect token not expected in this game", http.StatusForbidden)
		return
	}

	if gs.addPlayer(connectToken) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "Joined successfully. Players: %d/%d",
			len(gs.getJoinedTokens()), len(gs.expectedTokens))
	} else {
		http.Error(w, "Already joined", http.StatusConflict)
	}
}

// TCP handler for player registration
func (gs *GameServer) handleTCPConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	connectToken, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("Failed to read from TCP connection: %v", err)
		return
	}

	connectToken = strings.TrimSpace(connectToken)
	if connectToken == "" {
		conn.Write([]byte("ERROR: Connect token is required\n"))
		return
	}

	if !gs.expectedTokens[connectToken] {
		conn.Write([]byte("ERROR: Connect token not expected in this game\n"))
		return
	}

	if gs.addPlayer(connectToken) {
		response := fmt.Sprintf("OK: Joined successfully. Players: %d/%d\n",
			len(gs.getJoinedTokens()), len(gs.expectedTokens))
		conn.Write([]byte(response))
	} else {
		conn.Write([]byte("ERROR: Already joined\n"))
	}
}

func main() {
	var tokenID string
	var httpPort int
	var tcpPort int

	flag.StringVar(&tokenID, "token", "", "Match auth token used for /result/report (required)")
	flag.IntVar(&httpPort, "http-port", 8080, "HTTP server port")
	flag.IntVar(&tcpPort, "tcp-port", 8081, "TCP server port")
	flag.Parse()

	// Positional args are the per-player connect tokens — the credentials
	// each client presents on /join. In phase 1 these equal the players'
	// IDs; phase 2 will swap them for opaque generated secrets.
	connectTokens := flag.Args()

	if tokenID == "" {
		log.Fatal("Match auth token is required. Use -token flag.")
	}

	if len(connectTokens) == 0 {
		log.Fatal("At least one connect token is required.")
	}

	// Initialize game server
	gameServer := NewGameServer(tokenID, connectTokens)

	log.Printf("Starting example game server:")
	log.Printf("  Token ID: %s", tokenID)
	log.Printf("  Expected connect tokens: %v", gameServer.getExpectedTokens())
	log.Printf("  HTTP port: %d", httpPort)
	log.Printf("  TCP port: %d", tcpPort)

	// Create HTTP server with shutdown capability
	httpServer := &http.Server{
		Addr: ":" + strconv.Itoa(httpPort),
	}

	// Start HTTP server
	go func() {
		http.HandleFunc("/join", corsMiddleware(gameServer.handleHTTPJoin))
		http.HandleFunc("/health", corsMiddleware(gameServer.handleHealth))
		log.Printf("HTTP server listening on port %d", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Start TCP server
	go func() {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(tcpPort))
		if err != nil {
			log.Fatalf("Failed to start TCP server: %v", err)
		}
		defer listener.Close()

		log.Printf("TCP server listening on port %d", tcpPort)

		// Handle shutdown signal
		go func() {
			<-gameServer.shutdownChan
			log.Println("Shutting down TCP server...")
			listener.Close()
		}()

		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-gameServer.shutdownChan:
					log.Println("TCP server shutdown complete")
					return
				default:
					log.Printf("Failed to accept TCP connection: %v", err)
					continue
				}
			}
			go gameServer.handleTCPConnection(conn)
		}
	}()

	// Wait for shutdown signal
	<-gameServer.shutdownChan

	// Gracefully shutdown HTTP server
	log.Println("Shutting down HTTP server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	log.Println("Server shutdown complete")
}
