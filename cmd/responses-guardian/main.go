package main

import (
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const version = "0.4.1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// Returning errors lets startup cleanup run before main exits.
func run(args []string) (resultErr error) {
	flag := stdflag.NewFlagSet("responses-guardian", stdflag.ContinueOnError)
	listenAddr := flag.String("listen", "127.0.0.1:15722", "local listen address")
	upstreamRaw := flag.String("upstream", "http://127.0.0.1:15721", "upstream base URL")
	modeRaw := flag.String("mode", string(recoveryModeOnError), "initial mode: off, on-error or always")
	modeFile := flag.String("mode-file", "", "optional local JSON preference file for persistent live mode switching")
	settingsFile := flag.String("settings-file", "", "local JSON settings file; enables the control panel")
	instanceFile := flag.String("instance-file", "", "record active process settings separately from next-start settings")
	requestRetries := flag.Int("request-max-retries", 20, "Codex HTTP retry count (0-100)")
	streamRetries := flag.Int("stream-max-retries", 20, "Codex SSE retry count (0-100)")
	maxBody := flag.Int64("max-body-bytes", 64<<20, "maximum buffered Responses request size")
	manageConfig := flag.Bool("manage-config", false, "keep the selected Codex provider pointed at this proxy")
	restoreConfig := flag.Bool("restore-config", false, "restore the selected Codex provider to the CCSwitch URL, then exit")
	codexConfig := flag.String("codex-config", defaultCodexConfigPath(), "Codex config.toml path")
	provider := flag.String("provider", "custom", "Codex model provider section to manage")
	ccswitchBaseURL := flag.String("ccswitch-base-url", "http://127.0.0.1:15721/v1", "expected CCSwitch base_url in Codex config")
	guardianBaseURL := flag.String("guardian-base-url", "http://127.0.0.1:15722/v1", "guardian base_url written to Codex config")
	logFile := flag.String("log-file", "", "optional log file (request bodies and credentials are never logged)")
	pidFile := flag.String("pid-file", "", "optional PID file")
	showVersion := flag.Bool("version", false, "print version and exit")
	if err := flag.Parse(args); err != nil {
		if errors.Is(err, stdflag.ErrHelp) {
			return nil
		}
		return err
	}

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	mode, err := parseRecoveryMode(*modeRaw)
	if err != nil {
		return err
	}
	var instance *InstanceInfo
	if *manageConfig || *restoreConfig {
		instance, err = readInstance(*instanceFile)
		if err != nil {
			return fmt.Errorf("read active settings: %w", err)
		}
	}
	settings := AppSettings{Listen: *listenAddr, Upstream: *upstreamRaw, Provider: *provider, CodexConfig: *codexConfig, RequestRetries: *requestRetries, StreamRetries: *streamRetries}
	if *restoreConfig && instance != nil {
		// Pending settings may be invalid or refer to another provider. Restore
		// the recorded instance without loading those next-start preferences.
		settings = instance.Settings
	} else if *settingsFile != "" {
		*settingsFile, err = filepath.Abs(*settingsFile)
		if err != nil {
			return errors.New("invalid settings file path")
		}
		settings, err = readAppSettings(*settingsFile, settings)
		if err != nil {
			return fmt.Errorf("settings: %w", err)
		}
		// Explicit command-line flags take precedence over saved settings.
		flag.Visit(func(f *stdflag.Flag) {
			switch f.Name {
			case "listen":
				settings.Listen = *listenAddr
			case "upstream":
				settings.Upstream = *upstreamRaw
			case "provider":
				settings.Provider = *provider
			case "codex-config":
				settings.CodexConfig = *codexConfig
			case "request-max-retries":
				settings.RequestRetries = *requestRetries
			case "stream-max-retries":
				settings.StreamRetries = *streamRetries
			}
		})
	}
	if err := validateAppSettings(settings); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	*listenAddr, *upstreamRaw, *provider, *codexConfig = settings.Listen, settings.Upstream, settings.Provider, settings.CodexConfig
	explicit := map[string]bool{}
	flag.Visit(func(f *stdflag.Flag) { explicit[f.Name] = true })
	if !explicit["ccswitch-base-url"] {
		*ccswitchBaseURL = settings.ccswitchBaseURL()
	}
	if !explicit["guardian-base-url"] {
		*guardianBaseURL = settings.guardianBaseURL()
	}

	if *restoreConfig {
		manager := NewConfigManager(*codexConfig, *provider, *ccswitchBaseURL, *guardianBaseURL)
		changed, err := manager.RestoreCCSwitch()
		if err != nil {
			return fmt.Errorf("restore Codex config: %w", err)
		}
		if changed {
			log.Printf("restored %s provider %q to CCSwitch", *codexConfig, *provider)
		} else {
			log.Printf("Codex config already points away from Responses Guardian")
		}
		return nil
	}

	if *modeFile != "" {
		*modeFile, err = filepath.Abs(*modeFile)
		if err != nil {
			return errors.New("invalid mode file path")
		}
	}
	mode, err = initialMode(mode, *modeFile)
	if err != nil {
		return errors.New("could not load mode file; expected off, on-error or always")
	}

	closeLog, err := configureLogging(*logFile)
	if err != nil {
		return fmt.Errorf("configure logging: %w", err)
	}
	defer closeLog()

	upstream, err := url.Parse(*upstreamRaw)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return errors.New("invalid upstream URL")
	}
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return errors.New("upstream scheme must be http or https")
	}
	if !isLoopbackHost(upstream.Hostname()) {
		return errors.New("upstream must be loopback")
	}
	if err := validateLoopbackListen(*listenAddr); err != nil {
		return err
	}
	var configManager *ConfigManager
	var previous *AppSettings
	if instance != nil {
		previous = &instance.Settings
	}
	if *manageConfig {
		configManager = NewConfigManager(*codexConfig, *provider, *ccswitchBaseURL, *guardianBaseURL)
		if err := configManager.SetRetrySettings(settings.RequestRetries, settings.StreamRetries); err != nil {
			return err
		}
		if err := configManager.Preflight(previous); err != nil {
			return fmt.Errorf("prepare Codex config: %w", err)
		}
	}

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *listenAddr, err)
	}
	defer listener.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var configWatcherDone chan struct{}
	var startupChange *configChange
	started := false
	defer func() {
		cancel()
		if configWatcherDone != nil {
			<-configWatcherDone
		}
		var restoreErr error
		if !started {
			restoreErr = startupChange.Rollback()
		} else if configManager != nil {
			_, restoreErr = configManager.RestoreCCSwitch()
		}
		if restoreErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("restore Codex config: %w", restoreErr))
		}
	}()

	if err := writePIDFile(*pidFile); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	if *pidFile != "" {
		defer os.Remove(*pidFile)
	}

	guardian := NewGuardian(upstream, *maxBody, mode)
	guardian.modeFile = *modeFile
	if *settingsFile != "" {
		guardian.dashboard, err = NewDashboard(guardian, settings, *settingsFile, configManager)
		if err != nil {
			return fmt.Errorf("control panel: %w", err)
		}
	}
	if *settingsFile != "" {
		if err := saveAppSettings(*settingsFile, settings); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}
	if configManager != nil {
		startupChange, err = configManager.BeginStartup(previous)
		if err != nil {
			return fmt.Errorf("prepare Codex config: %w", err)
		}
	}
	if err := saveInstance(*instanceFile, settings); err != nil {
		return fmt.Errorf("save active settings: %w", err)
	}
	// Start watchers only after every startup file has been saved. On failure
	// above, rollback uses this attempt's exact snapshot, never an old instance.
	started = true
	if configManager != nil {
		configWatcherDone = make(chan struct{})
		go func() {
			defer close(configWatcherDone)
			configManager.Watch(ctx, 750*time.Millisecond)
		}()
	}
	if guardian.modeFile != "" {
		go guardian.watchModeFile(ctx)
	}
	server := &http.Server{
		Handler:           guardian.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("Responses Guardian %s listening on http://%s -> %s (mode=%s)", version, listener.Addr(), upstream, mode)
		serverErr <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		log.Printf("Responses Guardian stopped")
	case serveErr := <-serverErr:
		if serveErr != nil && serveErr != http.ErrServerClosed {
			return fmt.Errorf("HTTP server: %w", serveErr)
		}
	}
	return nil
}

func defaultCodexConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".codex", "config.toml")
}

func configureLogging(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	log.SetOutput(f)
	return func() { log.SetOutput(os.Stderr); _ = f.Close() }, nil
}

func writePIDFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
}

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("listen address must be loopback; refusing %q", host)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
