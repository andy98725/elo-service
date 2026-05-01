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

type GameServer struct {
	tokenID         string
	expectedPlayers map[string]bool
	joinedPlayers   map[string]bool
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

func NewGameServer(tokenID string, playerIDs []string) *GameServer {
	reportURL := os.Getenv("WEBSITE_URL")
	if reportURL == "" {
		reportURL = "https://elo-service.fly.dev/result/report"
	}

	expectedPlayers := make(map[string]bool)
	for _, playerID := range playerIDs {
		expectedPlayers[playerID] = true
	}

	return &GameServer{
		tokenID:         tokenID,
		expectedPlayers: expectedPlayers,
		joinedPlayers:   make(map[string]bool),
		reportURL:       reportURL,
		artifactURL:     platformBase(reportURL) + "/match/artifact",
		shutdownChan:    make(chan struct{}),
	}
}

func (gs *GameServer) addPlayer(playerID string) bool {
	gs.mutex.Lock()
	defer gs.mutex.Unlock()

	if !gs.expectedPlayers[playerID] {
		return false // Player not expected
	}

	if gs.joinedPlayers[playerID] {
		return false // Player already joined
	}

	gs.joinedPlayers[playerID] = true
	log.Printf("Player %s joined. Total players: %d/%d", playerID, len(gs.joinedPlayers), len(gs.expectedPlayers))

	// Check if all expected players have joined
	if len(gs.joinedPlayers) >= len(gs.expectedPlayers) {
		log.Println("All players have joined! Starting game...")
		go gs.reportResult()
		return true
	}

	return true
}

func (gs *GameServer) getPlayerList() []string {
	gs.mutex.RLock()
	defer gs.mutex.RUnlock()

	players := make([]string, 0, len(gs.joinedPlayers))
	for playerID := range gs.joinedPlayers {
		players = append(players, playerID)
	}
	return players
}

func (gs *GameServer) getExpectedPlayerList() []string {
	players := make([]string, 0, len(gs.expectedPlayers))
	for playerID := range gs.expectedPlayers {
		players = append(players, playerID)
	}
	return players
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
	players := gs.getPlayerList()

	log.Printf("Simulating game with %d players: %v", len(players), players)
	time.Sleep(3 * time.Second)

	// Randomly select winner
	winnerID := players[rand.Intn(len(players))]
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
	joinedCount := len(gs.joinedPlayers)
	expectedCount := len(gs.expectedPlayers)
	expectedPlayers := gs.getExpectedPlayerList()
	joinedPlayers := gs.getPlayerList()
	gs.mutex.RUnlock()

	response := map[string]interface{}{
		"status":           "healthy",
		"token_id":         gs.tokenID,
		"expected_players": expectedPlayers,
		"joined_players":   joinedPlayers,
		"player_count":     joinedCount,
		"expected_count":   expectedCount,
		"ready":            joinedCount >= expectedCount,
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

	playerID := strings.TrimSpace(string(body))
	if playerID == "" {
		http.Error(w, "Player ID is required", http.StatusBadRequest)
		return
	}

	if !gs.expectedPlayers[playerID] {
		http.Error(w, fmt.Sprintf("Player %s not expected in this game", playerID), http.StatusForbidden)
		return
	}

	if gs.addPlayer(playerID) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "Player %s joined successfully. Players: %d/%d",
			playerID, len(gs.getPlayerList()), len(gs.expectedPlayers))
	} else {
		http.Error(w, "Player already joined", http.StatusConflict)
	}
}

// TCP handler for player registration
func (gs *GameServer) handleTCPConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	playerID, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("Failed to read from TCP connection: %v", err)
		return
	}

	playerID = strings.TrimSpace(playerID)
	if playerID == "" {
		conn.Write([]byte("ERROR: Player ID is required\n"))
		return
	}

	if !gs.expectedPlayers[playerID] {
		conn.Write([]byte("ERROR: Player not expected in this game\n"))
		return
	}

	if gs.addPlayer(playerID) {
		response := fmt.Sprintf("OK: Player %s joined successfully. Players: %d/%d\n",
			playerID, len(gs.getPlayerList()), len(gs.expectedPlayers))
		conn.Write([]byte(response))
	} else {
		conn.Write([]byte("ERROR: Player already joined\n"))
	}
}

func main() {
	var tokenID string
	var httpPort int
	var tcpPort int

	flag.StringVar(&tokenID, "token", "", "Token ID (required)")
	flag.IntVar(&httpPort, "http-port", 8080, "HTTP server port")
	flag.IntVar(&tcpPort, "tcp-port", 8081, "TCP server port")
	flag.Parse()

	playerIDs := flag.Args() // Player IDs are remaining arguments

	if tokenID == "" {
		log.Fatal("Token ID is required. Use -token flag.")
	}

	if len(playerIDs) == 0 {
		log.Fatal("At least one player ID is required.")
	}

	// Initialize game server
	gameServer := NewGameServer(tokenID, playerIDs)

	log.Printf("Starting example game server:")
	log.Printf("  Token ID: %s", tokenID)
	log.Printf("  Expected players: %v", gameServer.getExpectedPlayerList())
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
