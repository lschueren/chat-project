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
var grid = make([][]string, 50) // Change from 1000 to 50
var cursors = make(map[*websocket.Conn]Cursor)
var port = 8080

type Message struct {
	Grid             [][]string        `json:"grid"`
	X                int               `json:"x"`
	Y                int               `json:"y"`
	Color            string            `json:"color"`
	TextColor        string            `json:"textColor,omitempty"`
	Cursors          map[string]Cursor `json:"cursors"`
	ConnectedClients int               `json:"connectedClients"`
	Bomb             *Bomb             `json:"bomb,omitempty"`
	Sequence         string            `json:"sequence,omitempty"`
	CheckSequences   bool              `json:"checkSequences,omitempty"`
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
		grid[i] = make([]string, 50) // Change from 1000 to 50
	}
	rand.Seed(time.Now().UnixNano())
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

	// Inside wsHandler function, update the message handling section:
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

		// Log received message
		fmt.Printf("Received message. Position: (%d, %d), CheckSequences: %v\n", msg.X, msg.Y, msg.CheckSequences)

		grid = msg.Grid
		cursors[conn] = Cursor{X: msg.X, Y: msg.Y, Color: cursors[conn].Color}
		msg.Cursors = serializeCursors(cursors)

		// Check sequences if flag is true (typing or clearing occurred)
		if msg.CheckSequences {
			fmt.Println("Checking all rows and columns for sequences...")

			// Check all rows
			for i := 0; i < len(grid); i++ {
				row := strings.Join(grid[i], "")
				fmt.Printf("Checking row %d: %q\n", i, row)

				if strings.Contains(row, "#bomb") {
					fmt.Printf("Found #bomb in row %d\n", i)
					go handleBomb()
					clearSequence(grid, "#bomb", i, msg.X)
					break
				} else if strings.Contains(row, "#fill") {
					fmt.Printf("Found #fill in row %d\n", i)
					go handleFill()
					clearSequence(grid, "#fill", i, msg.X)
					break
				}
			}

			// Check all columns
			for j := 0; j < len(grid[0]); j++ {
				var column []string
				for i := 0; i < len(grid); i++ {
					column = append(column, grid[i][j])
				}
				columnStr := strings.Join(column, "")
				fmt.Printf("Checking column %d: %q\n", j, columnStr)

				if strings.Contains(columnStr, "#bomb") {
					fmt.Printf("Found #bomb in column %d\n", j)
					go handleBomb()
					clearSequence(grid, "#bomb", msg.Y, j)
					break
				} else if strings.Contains(columnStr, "#fill") {
					fmt.Printf("Found #fill in column %d\n", j)
					go handleFill()
					clearSequence(grid, "#fill", msg.Y, j)
					break
				}
			}
		}

		cursorUpdates <- msg
	}
}

// Helper functions for min/max (Go < 1.21)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func handleFill() {

	patterns := []string{"█", "▒", "░", " "}

	// Pre-calculate random values for better performance
	randomVals := make([]float64, 1000)
	for i := range randomVals {
		randomVals[i] = rand.Float64()
	}

	// Fill grid in chunks
	chunkSize := 10
	for chunk := 0; chunk < 50; chunk += chunkSize {
		for i := chunk; i < min(chunk+chunkSize, 50); i++ {
			for j := 0; j < 50; j++ {
				switch {
				case i > 40:
					grid[i][j] = patterns[rand.Intn(2)]
				case i < 10:
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

	fmt.Println("Fill pattern completed and broadcast") // Debug log
}

func handleBomb() {
	const ExplosionSpeed = 50
	savedCursors := cursors // Save cursor positions

	// Color all letters red
	broadcast <- Message{
		Grid:      grid,
		TextColor: "red",
		Bomb:      &Bomb{Color: "red"},
		Cursors:   serializeCursors(savedCursors), // Keep cursors
	}

	time.Sleep(time.Duration(ExplosionSpeed) * time.Millisecond)

	// Color all letters green
	broadcast <- Message{
		Grid:      grid,
		TextColor: "green",
		Bomb:      &Bomb{Color: "green"},
		Cursors:   serializeCursors(savedCursors), // Keep cursors
	}

	// ... other color changes ...

	// Clear the grid but keep cursors
	for i := range grid {
		for j := range grid[i] {
			grid[i][j] = ""
		}
	}

	// Final broadcast with cleared grid but preserved cursors
	broadcast <- Message{
		Grid:    grid,
		Cursors: serializeCursors(savedCursors), // Keep cursors
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
		case <-ticker.C:
			// Keep the ticker for potential future use
			continue
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

			// Remove client safely
			client.Close()
			delete(clients, client)
			delete(cursors, client)

			// Update client count
			broadcastClientCount()
		}
	}
}

func clearSequence(grid [][]string, sequence string, row, col int) {
	fmt.Printf("Clearing sequence %q from grid at row %d, col %d\n", sequence, row, col)

	// Clear row
	rowStr := strings.Join(grid[row], "")
	if idx := strings.Index(rowStr, sequence); idx >= 0 {
		// Make sure we don't go beyond grid boundaries
		endIdx := min(idx+len(sequence), len(grid[row]))
		for i := idx; i < endIdx; i++ {
			grid[row][i] = ""
		}
		fmt.Printf("Cleared sequence from row %d (positions %d to %d)\n", row, idx, endIdx-1)
	}

	// Clear column
	var column []string
	for i := 0; i < len(grid); i++ {
		column = append(column, grid[i][col])
	}
	columnStr := strings.Join(column, "")
	if idx := strings.Index(columnStr, sequence); idx >= 0 {
		// Make sure we don't go beyond grid boundaries
		endIdx := min(idx+len(sequence), len(grid))
		for i := idx; i < endIdx; i++ {
			grid[i][col] = ""
		}
		fmt.Printf("Cleared sequence from column %d (positions %d to %d)\n", col, idx, endIdx-1)
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
