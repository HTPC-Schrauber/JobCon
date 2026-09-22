package main

import (
	"context"
	"flag"
	"fmt"
	"jobcon/internal/api"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/nexus"
	"jobcon/internal/runner"
	"jobcon/internal/storage"
	"jobcon/internal/web"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const Version = "1.0.0"

func main() {
	configPath := flag.String("config", "config.yaml", "Path to JobCon configuration file")
	showVersion := flag.Bool("version", false, "Show JobCon version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("JobCon v%s (Linux Single-Binary Engine)\n", Version)
		os.Exit(0)
	}

	log.Printf("=================================================================")
	log.Printf("⚡ Starting JobCon v%s (Talend Deployment & Execution Engine)", Version)
	log.Printf("=================================================================")

	// 1. Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Fatal configuration error: %v", err)
	}
	log.Printf("[Config] Loaded configuration. DB: %s, Logs: %s", cfg.Database.Path, cfg.Storage.LogsDir)

	// 2. Open SQLite Database
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Fatal database error: %v", err)
	}
	defer database.Close()
	log.Printf("[Database] SQLite WAL connection initialized successfully")

	// 3. Initialize Admin User
	adminPass, err := auth.EnsureAdminUser(database, cfg.Auth.AdminPassword)
	if err != nil {
		log.Fatalf("Failed to verify/initialize admin user: %v", err)
	}
	if adminPass != "" {
		log.Printf("*****************************************************************")
		log.Printf("🔑 INITIAL ADMIN USER CREATED:")
		log.Printf("   Username: admin")
		log.Printf("   Password: %s", adminPass)
		log.Printf("   (Please log in and change this password immediately!)")
		log.Printf("*****************************************************************")
	}

	// 4. Storage & Runner
	logStore, err := storage.NewLogStorage(cfg.Storage.LogsDir, cfg.Storage.CompressCompleted)
	if err != nil {
		log.Fatalf("Failed to initialize log storage: %v", err)
	}

	sshRunner := runner.NewSSHRunner(
		cfg.SSHDefaults.KeyPath,
		cfg.SSHDefaults.TimeoutSeconds,
		cfg.SSHDefaults.KeepaliveIntervalSeconds,
	)

	execManager := runner.NewExecutionManager(database, logStore, sshRunner, &cfg.Nexus)

	// 5. Auth & Middleware
	localAuth := auth.NewLocalAuthenticator(database)
	sessions := auth.NewSessionManager(24 * time.Hour)
	authMW := auth.NewMiddleware(localAuth, database, sessions)

	// 6. Nexus Syncer
	nexusSyncer := nexus.NewSyncer(database, cfg, func() *nexus.Client {
		baseURL, _ := database.GetSetting("nexus_base_url", cfg.Nexus.BaseURL)
		username, _ := database.GetSetting("nexus_username", cfg.Nexus.Username)
		password, _ := database.GetSetting("nexus_password", cfg.Nexus.Password)
		return nexus.NewClient(baseURL, username, password)
	})

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	go nexusSyncer.StartScheduler(bgCtx)

	// 7. Router & Handlers
	mux := http.NewServeMux()

	apiHandler := api.NewAPI(database, logStore, execManager, sshRunner, authMW, cfg, nexusSyncer)
	apiHandler.RegisterRoutes(mux)

	webHandler, err := web.NewWebHandler(database, execManager, logStore, authMW, localAuth, sessions, cfg, nexusSyncer)
	if err != nil {
		log.Fatalf("Failed to initialize web UI: %v", err)
	}
	webHandler.RegisterRoutes(mux)

	// Wrap root with global auth context injection and logging
	rootHandler := authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	}))

	// 7. Server Setup
	addr := fmt.Sprintf("%s:%d", cfg.Server.Bind, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      rootHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Zero to allow infinite SSE log streaming
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown channel
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		if cfg.TLS.Enabled {
			log.Printf("[Server] Listening securely with TLS on https://%s", addr)
			if err := srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("TLS Server failed: %v", err)
			}
		} else {
			log.Printf("[Server] Listening on http://%s", addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server failed: %v", err)
			}
		}
	}()

	<-stop
	log.Printf("[Server] Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Server] Forced shutdown: %v", err)
	}
	log.Printf("[Server] JobCon stopped.")
}
