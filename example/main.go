package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

type RequestBody struct {
	TokenID  string `json:"token_id"`
	WinnerID string `json:"winner_id"`
}

func main() {
	website := os.Getenv("WEBSITE_URL")
	if website == "" {
		website = "https://elo-service.fly.dev/result/report"
	}

	var tokenID string // Token ID is required
	flag.StringVar(&tokenID, "token", "", "Token ID (required)")
	flag.Parse()
	playerIDs := flag.Args() // Player IDs are remaining arguments

	if tokenID == "" {
		log.Fatal("Token ID is required. Use -token flag.")
	}
	if len(playerIDs) == 0 {
		log.Fatal("At least one player ID is required.")
	}

	log.Printf("Starting example game server with token %s and %d players", tokenID, len(playerIDs))
	log.Printf("Player IDs: %v", playerIDs)

	log.Println("Waiting 5 seconds...")
	time.Sleep(5 * time.Second)

	winnerID := playerIDs[rand.Intn(len(playerIDs))]
	jsonData, err := json.Marshal(RequestBody{
		TokenID:  tokenID,
		WinnerID: winnerID,
	})
	if err != nil {
		log.Fatalf("Failed to marshal JSON: %v", err)
	}

	log.Printf("Sending request to %s with token %s and winner %s", website, tokenID, winnerID)
	resp, err := http.Post(website, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed to read response body: %v", err)
	}

	log.Printf("Request sent successfully. Status: %s, Body: %s", resp.Status, string(body))
}
