// Command simulator serves the Norrköping Simulator: a local city simulation for the UI in
// ../norrkoping-simulator-ui. The map data is embedded, so the binary runs on its own.
//
//	HOST            address to bind, default 127.0.0.1
//	PORT            port to bind, default 8080
//	ALLOWED_ORIGIN  browser origin that may send commands, default http://localhost:5173
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"norrkoping-simulator/internal/api"
	"norrkoping-simulator/internal/sim"
)

func main() {
	started := time.Now()
	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 8080
	if p, err := strconv.Atoi(os.Getenv("PORT")); err == nil {
		port = p
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	city := sim.NewCityService()
	drones := sim.NewDroneService()
	go city.Run(ctx)
	go drones.Run(ctx)

	address := net.JoinHostPort(host, strconv.Itoa(port))
	server := &http.Server{
		Addr:              address,
		Handler:           api.NewServer(city, drones).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// The streams send for as long as the client listens; requests still end with the server.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("Norrköping Simulator is listening on http://%s (ready in %d ms)", address, time.Since(started).Milliseconds())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
