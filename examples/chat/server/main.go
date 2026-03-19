package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"

	"github.com/marcomoesman/fastwire"
)

type ChatServer struct {
	fastwire.BaseHandler
	mu    sync.RWMutex
	conns map[*fastwire.Connection]string // conn → nickname
}

func (s *ChatServer) OnConnect(conn *fastwire.Connection) {
	s.mu.Lock()
	s.conns[conn] = conn.RemoteAddr().String()
	s.mu.Unlock()
	log.Printf("connected: %s", conn.RemoteAddr())
}

func (s *ChatServer) OnDisconnect(conn *fastwire.Connection, reason fastwire.DisconnectReason) {
	s.mu.Lock()
	nick := s.conns[conn]
	delete(s.conns, conn)
	s.mu.Unlock()

	log.Printf("disconnected: %s (%s)", nick, reason)
	s.broadcast(conn, fmt.Sprintf("* %s left the chat", nick))
}

func (s *ChatServer) OnMessage(conn *fastwire.Connection, data []byte, channel byte) {
	msg := string(data)

	// Check for /nick command.
	if len(msg) > 6 && msg[:6] == "/nick " {
		newNick := msg[6:]
		s.mu.Lock()
		oldNick := s.conns[conn]
		s.conns[conn] = newNick
		s.mu.Unlock()
		log.Printf("rename: %s → %s", oldNick, newNick)
		conn.Send([]byte(fmt.Sprintf("* You are now known as %s", newNick)), 0)
		s.broadcast(conn, fmt.Sprintf("* %s is now known as %s", oldNick, newNick))
		return
	}

	s.mu.RLock()
	nick := s.conns[conn]
	s.mu.RUnlock()

	log.Printf("[%s] %s", nick, msg)
	s.broadcast(conn, fmt.Sprintf("[%s] %s", nick, msg))
}

// broadcast sends a message to all connected clients except the sender.
func (s *ChatServer) broadcast(sender *fastwire.Connection, msg string) {
	data := []byte(msg)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn := range s.conns {
		if conn != sender {
			conn.Send(data, 0)
		}
	}
}

func main() {
	addr := ":7777"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	handler := &ChatServer{conns: make(map[*fastwire.Connection]string)}
	srv, err := fastwire.NewServer(addr, fastwire.DefaultServerConfig(), handler)
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		log.Fatal(err)
	}
	defer srv.Stop()

	fmt.Printf("Chat server listening on %s\n", srv.Addr())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	fmt.Println("\nShutting down...")
}
