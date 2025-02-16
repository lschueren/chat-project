package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var server *http.Server
var upgrader = websocket.Upgrader{}
var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan Message)
var grid = make([][]string, 30)
var cursors = make(map[*websocket.Conn]Cursor)

type Message struct {
	Grid    [][]string        `json:"grid"`
	X       int               `json:"x"`
	Y       int               `json:"y"`
	Color   string            `json:"color"`
	Cursors map[string]Cursor `json:"cursors"`
}

type Cursor struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Color string `json:"color"`
}

func init() {
	for i := range grid {
		grid[i] = make([]string, 30)
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()
	clients[conn] = true

	color := "red"
	if len(clients)%2 == 0 {
		color = "green"
	}
	cursors[conn] = Cursor{X: 0, Y: 0, Color: color}

	// Send initial state to the client
	initialMessage := Message{
		Grid:    grid,
		X:       0,
		Y:       0,
		Color:   color,
		Cursors: serializeCursors(cursors),
	}
	err = conn.WriteJSON(initialMessage)
	if err != nil {
		fmt.Println("WriteJSON error:", err)
		conn.Close()
		delete(clients, conn)
		delete(cursors, conn)
		return
	}

	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			fmt.Println("ReadJSON error:", err)
			delete(clients, conn)
			delete(cursors, conn)
			break
		}
		grid = msg.Grid
		cursors[conn] = Cursor{X: msg.X, Y: msg.Y, Color: cursors[conn].Color} // Preserve the original color
		msg.Cursors = serializeCursors(cursors)
		broadcast <- msg
	}
}

func handleMessages() {
	for {
		msg := <-broadcast
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
}

func serializeCursors(cursors map[*websocket.Conn]Cursor) map[string]Cursor {
	serialized := make(map[string]Cursor)
	for conn, cursor := range cursors {
		serialized[conn.RemoteAddr().String()] = cursor
	}
	return serialized
}

func main() {
	server = &http.Server{Addr: ":8080"}

	http.HandleFunc("/ws", wsHandler)
	http.Handle("/", http.FileServer(http.Dir("./")))

	go handleMessages()

	go func() {
		fmt.Println("Starting server at :8080")
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
