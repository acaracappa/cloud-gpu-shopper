package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/mockprovider"
)

func main() {
	addr := flag.String("addr", ":8888", "Server address")
	flag.Parse()

	state := mockprovider.NewState()
	server := mockprovider.NewServer(state)

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down mock provider...")
		os.Exit(0)
	}()

	log.Printf("Starting mock Vast.ai provider on %s", *addr)
	if err := server.Run(*addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
