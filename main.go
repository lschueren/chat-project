package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var server *http.Server
var upgrader = websocket.Upgrader{}
var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan Message)
var grid = make([][]string, 1000) // Change from 50 to 1000
var cursors = make(map[*websocket.Conn]Cursor)
var port = 8080

// Add message buffer to reduce broadcasts
const messageBufferSize = 10

var messageBuffer = make(chan Message, messageBufferSize)

type Message struct {
	Grid             [][]string        `json:"grid"`
	X                int               `json:"x"`
	Y                int               `json:"y"`
	Color            string            `json:"color"`
	TextColor        string            `json:"textColor,omitempty"`
	Cursors          map[string]Cursor `json:"cursors"`
	ConnectedClients int               `json:"connectedClients"`
	Bomb             *Bomb             `json:"bomb,omitempty"`
}

type Cursor struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Color string `json:"color"`
}

type Bomb struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Color string `json:"color"`
}

func init() {
	for i := range grid {
		grid[i] = make([]string, 1000) // Change from 50 to 1000
	}
	//rand.Seed(time.Now().UnixNano())
}

func getRandomColor() string {
	colors := []string{"blue", "yellow", "purple", "orange", "pink", "brown"}
	return colors[rand.Intn(len(colors))]
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()
	clients[conn] = true
	broadcastClientCount()

	var color string
	if len(clients) == 1 {
		color = "red"
	} else if len(clients) == 2 {
		color = "green"
	} else {
		color = getRandomColor()
	}
	cursors[conn] = Cursor{X: 0, Y: 0, Color: color}

	// Send initial state to the client
	initialMessage := Message{
		Grid:             grid,
		X:                0,
		Y:                0,
		Color:            color,
		Cursors:          serializeCursors(cursors),
		ConnectedClients: len(clients),
	}
	err = conn.WriteJSON(initialMessage)
	if err != nil {
		fmt.Println("WriteJSON error:", err)
		conn.Close()
		delete(clients, conn)
		delete(cursors, conn)
		broadcastClientCount()
		return
	}

	// Use buffered channel for cursor updates
	cursorUpdates := make(chan Message, 10)
	defer close(cursorUpdates)

	// Handle cursor updates in separate goroutine
	go func() {
		ticker := time.NewTicker(16 * time.Millisecond)
		defer ticker.Stop()

		var lastUpdate Message
		for {
			select {
			case update := <-cursorUpdates:
				lastUpdate = update
			case <-ticker.C:
				if lastUpdate.Grid != nil {
					broadcast <- lastUpdate
					lastUpdate = Message{}
				}
			}
		}
	}()

	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			fmt.Println("ReadJSON error:", err)
			delete(clients, conn)
			delete(cursors, conn)
			broadcastClientCount()
			break
		}

		grid = msg.Grid
		cursors[conn] = Cursor{X: msg.X, Y: msg.Y, Color: cursors[conn].Color}
		msg.Cursors = serializeCursors(cursors)

		// Send update to buffer instead of directly to broadcast
		cursorUpdates <- msg

		// Check sequences only when needed
		if strings.Contains(msg.Grid[msg.X][msg.Y], "#") {
			if detectBombSequence(grid, msg.X, msg.Y) {
				go handleBomb()
			} else if detectFillSequence(grid) {
				go handleFill()
			}
		}
	}
}

// Helper functions for min/max (Go < 1.21)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func detectBombSequence(grid [][]string, x, y int) bool {
	sequence := "#bomb"
	// Use string builder for better performance
	var sb strings.Builder

	// Only check recent changes around cursor position
	startX := max(0, x-25)
	endX := min(len(grid), x+25)
	startY := max(0, y-25)
	endY := min(len(grid[0]), y+25)

	// Check rows in viewport
	for i := startX; i < endX; i++ {
		sb.Reset()
		for j := startY; j < endY; j++ {
			if grid[i][j] != "" {
				sb.WriteString(grid[i][j])
			}
		}
		if strings.Contains(sb.String(), sequence) {
			return true
		}
	}

	// Check columns in viewport
	for j := startY; j < endY; j++ {
		sb.Reset()
		for i := startX; i < endX; i++ {
			if grid[i][j] != "" {
				sb.WriteString(grid[i][j])
			}
		}
		if strings.Contains(sb.String(), sequence) {
			return true
		}
	}
	return false
}

// Add new function to detect fill sequence
func detectFillSequence(grid [][]string) bool {
	sequence := "#fill"
	// Check rows only within viewport
	for i := 0; i < len(grid); i++ {
		// Only join the viewport portion of the row
		row := strings.Join(grid[i], "")
		if strings.Contains(row, sequence) {
			return true
		}
	}

	// Check columns only within viewport
	for j := 0; j < len(grid[0]); j++ {
		var column []string
		for i := 0; i < len(grid); i++ {
			column = append(column, grid[i][j])
		}
		if strings.Contains(strings.Join(column, ""), sequence) {
			return true
		}
	}
	return false
}

// Add new function to handle fill action
func handleFill() {
	const FillSpeed = 50
	patterns := []string{"█", "▒", "░", " "}

	// Pre-calculate random values for better performance
	randomVals := make([]float64, 1000)
	for i := range randomVals {
		randomVals[i] = rand.Float64()
	}

	// Fill grid in chunks
	chunkSize := 100
	for chunk := 0; chunk < 1000; chunk += chunkSize {
		for i := chunk; i < min(chunk+chunkSize, 1000); i++ {
			for j := 0; j < 1000; j++ {
				switch {
				case i > 800:
					grid[i][j] = patterns[rand.Intn(2)]
				case i < 200:
					grid[i][j] = patterns[rand.Intn(2)+2]
				default:
					if randomVals[i] < 0.3 {
						pattern := patterns[rand.Intn(len(patterns))]
						grid[i][j] = pattern
					} else {
						grid[i][j] = patterns[rand.Intn(len(patterns))]
					}
				}
			}
		}

		// Broadcast chunk update
		broadcast <- Message{
			Grid:      grid,
			TextColor: "#2F4F4F",
		}
	}
}

func handleBomb() {

	const ExplosionSpeed = 50

	// Color all letters red
	broadcast <- Message{
		Grid:      grid,
		TextColor: "red",
		Bomb:      &Bomb{Color: "red"},
	}

	// Wait
	time.Sleep(time.Duration(ExplosionSpeed) * time.Millisecond)

	// Color all letters green
	broadcast <- Message{
		Grid:      grid,
		TextColor: "green",
		Bomb:      &Bomb{Color: "green"},
	}

	// Wait
	time.Sleep(time.Duration(ExplosionSpeed) * time.Millisecond)

	// Color all letters blue
	broadcast <- Message{
		Grid:      grid,
		TextColor: "blue",
		Bomb:      &Bomb{Color: "blue"},
	}

	// Wait
	time.Sleep(time.Duration(ExplosionSpeed) * time.Millisecond)

	// Color all letters yellow
	broadcast <- Message{
		Grid:      grid,
		TextColor: "yellow",
		Bomb:      &Bomb{Color: "yellow"},
	}

	// Wait
	time.Sleep(time.Duration(ExplosionSpeed) * time.Millisecond)

	// Color all letters pink
	broadcast <- Message{
		Grid:      grid,
		TextColor: "pink",
		Bomb:      &Bomb{Color: "pink"},
	}

	// Wait
	time.Sleep(time.Duration(ExplosionSpeed) * time.Millisecond)

	// Clear the grid
	for i := range grid {
		for j := range grid[i] {
			grid[i][j] = ""
		}
	}
	// Wait
	time.Sleep(ExplosionSpeed * time.Millisecond)

	// Broadcast the updated grid to all clients
	broadcast <- Message{
		Grid: grid,
	}
}

func handleMessages() {
	var lastMsg Message
	ticker := time.NewTicker(16 * time.Millisecond) // ~60fps
	defer ticker.Stop()

	for {
		select {
		case msg := <-broadcast:
			lastMsg = msg
		case <-ticker.C:
			if lastMsg.Grid != nil {
				lastMsg.ConnectedClients = len(clients)
				for client := range clients {
					err := client.WriteJSON(lastMsg)
					if err != nil {
						client.Close()
						delete(clients, client)
						delete(cursors, client)
					}
				}
				lastMsg = Message{} // Reset last message
			}
		}
	}
}

func serializeCursors(cursors map[*websocket.Conn]Cursor) map[string]Cursor {
	serialized := make(map[string]Cursor)
	for conn, cursor := range cursors {
		serialized[conn.RemoteAddr().String()] = cursor
	}
	return serialized
}

func broadcastClientCount() {
	msg := Message{
		ConnectedClients: len(clients),
	}
	for client := range clients {
		err := client.WriteJSON(msg)
		if err != nil {
			fmt.Println("WriteJSON error:", err)
			client.Close()
			delete(clients, client)
			delete(cursors, client)
		}
	}
}

func main() {
	server = &http.Server{Addr: fmt.Sprintf(":%d", port)}

	http.HandleFunc("/ws", wsHandler)
	http.Handle("/", http.FileServer(http.Dir("./")))

	go handleMessages()

	go func() {
		fmt.Printf("Starting server at :%d\n", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("ListenAndServe(): %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Printf("Server forced to shutdown: %s\n", err)
	}

	fmt.Println("Server exiting")
	os.Exit(0)
}
