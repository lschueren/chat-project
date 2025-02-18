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

var viewportX = 0
var viewportY = 0
var viewportSize = 50 // Size of the visible area

type Message struct {
	Grid             [][]string        `json:"grid"`
	X                int               `json:"x"`
	Y                int               `json:"y"`
	ViewportX        int               `json:"viewportX"`
	ViewportY        int               `json:"viewportY"`
	ViewportSize     int               `json:"viewportSize"`
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
		cursors[conn] = Cursor{X: msg.X, Y: msg.Y, Color: cursors[conn].Color} // Preserve the original color
		msg.Cursors = serializeCursors(cursors)
		broadcast <- msg

		// Check for sequences
		if detectBombSequence(grid) {
			fmt.Println("Bomb detected in the grid")
			go handleBomb()
		} else if detectFillSequence(grid) {
			fmt.Println("Fill sequence detected in the grid")
			go handleFill()
		}
	}
}

func detectBombSequence(grid [][]string) bool {
	sequence := "#bomb"
	// Check rows
	for i := range grid {
		row := strings.Join(grid[i], "")
		fmt.Println("Checking row:", row) // Debugging statement
		if strings.Contains(row, sequence) {
			return true
		}
	}
	// Check columns
	for j := 0; j < len(grid[0]); j++ {
		var column []string
		for i := 0; i < len(grid); i++ {
			column = append(column, grid[i][j])
		}
		columnStr := strings.Join(column, "")
		fmt.Println("Checking column:", columnStr) // Debugging statement
		if strings.Contains(columnStr, sequence) {
			return true
		}
	}
	return false
}

// Add new function to detect fill sequence
func detectFillSequence(grid [][]string) bool {
	sequence := "#fill"
	// Check rows
	for i := range grid {
		row := strings.Join(grid[i], "")
		if strings.Contains(row, sequence) {
			return true
		}
	}
	// Check columns
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

	// RPG-style patterns
	patterns := []string{
		"█", // Full block (mountains/walls)
		"▒", // Medium shade (grass/trees)
		"░", // Light shade (sand/paths)
		" ", // Empty space (clearings)
	}

	// Fill the entire 1000x1000 grid
	for i := range grid {
		for j := range grid[i] {
			// Adjust position calculations for larger grid
			if i > 800 { // Bottom 20% - mountains
				grid[i][j] = patterns[rand.Intn(2)]
			} else if i < 200 { // Top 20% - sky
				grid[i][j] = patterns[rand.Intn(2)+2]
			} else { // Middle area
				if rand.Float64() < 0.3 {
					pattern := patterns[rand.Intn(len(patterns))]
					grid[i][j] = pattern
					if j > 0 && rand.Float64() < 0.7 {
						grid[i][j-1] = pattern
					}
					if i > 0 && rand.Float64() < 0.7 {
						grid[i-1][j] = pattern
					}
				} else {
					grid[i][j] = patterns[rand.Intn(len(patterns))]
				}
			}
		}
	}

	// Retro color sequence (landscape colors)
	colors := []string{
		"#2F4F4F", // Dark slate gray (base)
		"#228B22", // Forest green
		"#8B4513", // Saddle brown
		"#4B0082", // Indigo
		"#483D8B", // Dark slate blue
	}

	for _, color := range colors {
		broadcast <- Message{
			Grid:      grid,
			TextColor: color,
		}
		time.Sleep(time.Duration(FillSpeed) * time.Millisecond)
	}

	// Set final color to forest green for landscape feel
	broadcast <- Message{
		Grid:      grid,
		TextColor: "#228B22",
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
	for {
		msg := <-broadcast
		msg.ConnectedClients = len(clients)
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				fmt.Println("WriteJSON error:", err)
				client.Close()
				delete(clients, client)
				delete(cursors, client)
				broadcastClientCount()
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
