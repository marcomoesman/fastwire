package main

import (
	"bufio"
	"fmt"
	"log"
	"os"

	"github.com/marcomoesman/fastwire"
)

type ChatClient struct {
	fastwire.BaseHandler
	done chan struct{}
}

func (c *ChatClient) OnConnect(conn *fastwire.Connection) {
	fmt.Println("Connected! Type /nick <name> to set your nickname, or just start chatting.")
}

func (c *ChatClient) OnDisconnect(_ *fastwire.Connection, reason fastwire.DisconnectReason) {
	fmt.Printf("\nDisconnected: %s\n", reason)
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

func (c *ChatClient) OnMessage(_ *fastwire.Connection, data []byte, _ byte) {
	fmt.Printf("\r%s\n> ", data)
}

func main() {
	addr := "127.0.0.1:7777"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	handler := &ChatClient{done: make(chan struct{})}
	cli, err := fastwire.NewClient(fastwire.DefaultClientConfig(), handler)
	if err != nil {
		log.Fatal(err)
	}
	if err := cli.Connect(addr); err != nil {
		log.Fatal(err)
	}

	// Read stdin in a goroutine so we can also watch for server disconnect.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Print("> ")
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				fmt.Print("> ")
				continue
			}
			if line == "/quit" {
				fmt.Println("Goodbye!")
				cli.Close()
				return
			}
			conn := cli.Connection()
			if conn == nil {
				return
			}
			if err := conn.Send([]byte(line), 0); err != nil {
				fmt.Printf("send error: %v\n", err)
				cli.Close()
				return
			}
			fmt.Print("> ")
		}
		// EOF (e.g. Ctrl-D).
		cli.Close()
	}()

	<-handler.done
}
