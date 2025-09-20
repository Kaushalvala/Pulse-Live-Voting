package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var (
	ctx      = context.Background()
	rdb      *redis.Client
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins in development
		},
	}

	// WebSocket connection management
	connections = make(map[string]map[*websocket.Conn]bool)
	connMutex   sync.RWMutex
)

// Poll represents a poll structure
type Poll struct {
	ID       string            `json:"id"`
	Question string            `json:"question"`
	Options  map[string]string `json:"options"`
	Votes    map[string]int    `json:"votes"`
}

// CreatePollRequest represents the request body for creating a poll
type CreatePollRequest struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// VoteMessage represents a vote sent via WebSocket
type VoteMessage struct {
	Vote     string `json:"vote"`
	ClientID string `json:"clientId"`
}

// UpdateMessage represents vote count updates
type UpdateMessage struct {
	Type  string         `json:"type"`
	Votes map[string]int `json:"votes"`
}

func main() {
	// Initialize Redis client
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password
		DB:       0,  // default DB
	})

	// Test Redis connection
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatal("Failed to connect to Redis:", err)
	}
	log.Println("Connected to Redis")

	// Start the pub/sub listener
	go listenToPubSub()

	// Set up routes
	r := mux.NewRouter()

	// API routes
	r.HandleFunc("/api/poll", createPoll).Methods("POST")
	r.HandleFunc("/api/poll/{pollID}", getPoll).Methods("GET")

	// WebSocket route
	r.HandleFunc("/ws/{pollID}", handleWebSocket)

	// Static file routes
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./static/")))

	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}

// generateID creates a random 6-character ID
func generateID() string {
	bytes := make([]byte, 3)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// createPoll handles POST /api/poll
func createPoll(w http.ResponseWriter, r *http.Request) {
	var req CreatePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Question == "" || len(req.Options) < 2 {
		http.Error(w, "Question and at least 2 options required", http.StatusBadRequest)
		return
	}

	// Generate unique poll ID
	pollID := generateID()
	pollKey := fmt.Sprintf("poll:%s", pollID)

	// Create Redis hash fields
	fields := map[string]interface{}{
		"question": req.Question,
	}

	for i, option := range req.Options {
		optionKey := fmt.Sprintf("option_%d", i)
		voteKey := fmt.Sprintf("votes_%d", i)
		fields[optionKey] = option
		fields[voteKey] = 0
	}

	// Save to Redis
	if err := rdb.HMSet(ctx, pollKey, fields).Err(); err != nil {
		log.Printf("Failed to save poll: %v", err)
		http.Error(w, "Failed to create poll", http.StatusInternalServerError)
		return
	}

	// Set expiration (24 hours)
	rdb.Expire(ctx, pollKey, 24*time.Hour)

	// Track voted clients in a separate set
	votedKey := fmt.Sprintf("voted:%s", pollID)
	rdb.Del(ctx, votedKey) // Clear any existing data
	rdb.Expire(ctx, votedKey, 24*time.Hour)

	// Return the poll ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":  pollID,
		"url": fmt.Sprintf("/poll.html?id=%s", pollID),
	})
}

// getPoll handles GET /api/poll/{pollID}
func getPoll(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pollID := vars["pollID"]
	pollKey := fmt.Sprintf("poll:%s", pollID)

	// Get all fields from Redis hash
	data, err := rdb.HGetAll(ctx, pollKey).Result()
	if err != nil || len(data) == 0 {
		http.Error(w, "Poll not found", http.StatusNotFound)
		return
	}

	// Parse the data
	poll := Poll{
		ID:       pollID,
		Question: data["question"],
		Options:  make(map[string]string),
		Votes:    make(map[string]int),
	}

	// Extract options and votes
	for key, value := range data {
		if strings.HasPrefix(key, "option_") {
			optionID := strings.TrimPrefix(key, "option_")
			poll.Options[optionID] = value
		} else if strings.HasPrefix(key, "votes_") {
			optionID := strings.TrimPrefix(key, "votes_")
			var votes int
			fmt.Sscanf(value, "%d", &votes)
			poll.Votes[optionID] = votes
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(poll)
}

// handleWebSocket handles WebSocket connections
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pollID := vars["pollID"]

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Add connection to the pool
	connMutex.Lock()
	if connections[pollID] == nil {
		connections[pollID] = make(map[*websocket.Conn]bool)
	}
	connections[pollID][conn] = true
	connMutex.Unlock()

	// Remove connection when done
	defer func() {
		connMutex.Lock()
		delete(connections[pollID], conn)
		if len(connections[pollID]) == 0 {
			delete(connections, pollID)
		}
		connMutex.Unlock()
	}()

	// Send current vote counts to new connection
	sendCurrentVotes(conn, pollID)

	// Listen for messages from this client
	for {
		var msg VoteMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Process vote
		if msg.Vote != "" && msg.ClientID != "" {
			handleVote(pollID, msg.Vote, msg.ClientID)
		}
	}
}

// handleVote processes a vote
func handleVote(pollID, optionID, clientID string) {
	pollKey := fmt.Sprintf("poll:%s", pollID)
	votedKey := fmt.Sprintf("voted:%s", pollID)

	// Check if client already voted
	exists, err := rdb.SIsMember(ctx, votedKey, clientID).Result()
	if err != nil {
		log.Printf("Error checking vote status: %v", err)
		return
	}
	if exists {
		log.Printf("Client %s already voted for poll %s", clientID, pollID)
		return
	}

	// Increment vote count atomically
	voteKey := fmt.Sprintf("votes_%s", optionID)
	newCount, err := rdb.HIncrBy(ctx, pollKey, voteKey, 1).Result()
	if err != nil {
		log.Printf("Failed to increment vote: %v", err)
		return
	}

	// Mark client as voted
	rdb.SAdd(ctx, votedKey, clientID)

	log.Printf("Vote recorded: poll=%s, option=%s, newCount=%d", pollID, optionID, newCount)

	// Get all current votes
	votes := getCurrentVotes(pollID)

	// Publish update to Redis channel
	updateMsg, _ := json.Marshal(UpdateMessage{
		Type:  "voteUpdate",
		Votes: votes,
	})

	channel := fmt.Sprintf("updates:%s", pollID)
	if err := rdb.Publish(ctx, channel, updateMsg).Err(); err != nil {
		log.Printf("Failed to publish update: %v", err)
	}
}

// getCurrentVotes gets all current vote counts for a poll
func getCurrentVotes(pollID string) map[string]int {
	pollKey := fmt.Sprintf("poll:%s", pollID)
	data, err := rdb.HGetAll(ctx, pollKey).Result()
	if err != nil {
		return nil
	}

	votes := make(map[string]int)
	for key, value := range data {
		if strings.HasPrefix(key, "votes_") {
			optionID := strings.TrimPrefix(key, "votes_")
			var count int
			fmt.Sscanf(value, "%d", &count)
			votes[optionID] = count
		}
	}
	return votes
}

// sendCurrentVotes sends current vote counts to a specific connection
func sendCurrentVotes(conn *websocket.Conn, pollID string) {
	votes := getCurrentVotes(pollID)
	msg := UpdateMessage{
		Type:  "voteUpdate",
		Votes: votes,
	}
	conn.WriteJSON(msg)
}

// listenToPubSub subscribes to Redis pub/sub channels
func listenToPubSub() {
	pubsub := rdb.PSubscribe(ctx, "updates:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		// Extract poll ID from channel name
		parts := strings.Split(msg.Channel, ":")
		if len(parts) != 2 {
			continue
		}
		pollID := parts[1]

		// Broadcast to all connected clients for this poll
		broadcastToClients(pollID, msg.Payload)
	}
}

// broadcastToClients sends a message to all WebSocket clients for a poll
func broadcastToClients(pollID string, message string) {
	connMutex.RLock()
	conns := connections[pollID]
	connMutex.RUnlock()

	if conns == nil {
		return
	}

	var update UpdateMessage
	if err := json.Unmarshal([]byte(message), &update); err != nil {
		log.Printf("Failed to unmarshal update message: %v", err)
		return
	}

	connMutex.RLock()
	defer connMutex.RUnlock()

	for conn := range conns {
		if err := conn.WriteJSON(update); err != nil {
			log.Printf("Failed to send update to client: %v", err)
		}
	}
}
