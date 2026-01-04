package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/zalo/moonparty/internal/server"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.json", "Path to configuration file")
	sunshineHost := flag.String("host", "localhost", "Sunshine host address")
	sunshinePort := flag.Int("port", 47989, "Sunshine Moonlight API port (not 47990 web UI)")
	listenAddr := flag.String("listen", ":8080", "Web server listen address")
	flag.Parse()

	// Create configuration
	cfg := &server.Config{
		ListenAddr:   *listenAddr,
		SunshineHost: *sunshineHost,
		SunshinePort: *sunshinePort,
		ConfigPath:   *configPath,
		ICEServers: []string{
			"stun:stun.l.google.com:19302",
			"stun:stun1.l.google.com:19302",
		},
	}

	// Create and start server
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")
		srv.Shutdown()
	}()

	// Start the server
	log.Printf("Moonparty starting on %s", *listenAddr)
	log.Printf("Connecting to Sunshine at %s:%d", *sunshineHost, *sunshinePort)

	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
