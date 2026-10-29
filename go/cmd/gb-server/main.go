package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/game"
	gbnet "github.com/phreddrick/GB/go/internal/net"
)

func main() {
	cfgPath := flag.String("config", "", "path to JSON config file (defaults to built-in values)")
	flag.Parse()

	cfg := config.Default()
	if *cfgPath != "" {
		f, err := os.Open(*cfgPath)
		if err != nil {
			log.Fatalf("open config: %v", err)
		}
		if err := json.NewDecoder(f).Decode(cfg); err != nil {
			log.Fatalf("parse config: %v", err)
		}
		f.Close()
	}

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	gameState, err := game.New(cfg, store)
	if err != nil {
		log.Fatalf("init game state: %v", err)
	}

	srv := gbnet.New(cfg, gameState)

	// Wire up the session registry so the game can push notifications.
	gameState.SetRegistry(srv.Registry())

	if err := srv.Run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
