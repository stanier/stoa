package daemon

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stanier/stoa/internal/logging"
)

// ServeHTTP launches srv in a background goroutine, waits for SIGINT or SIGTERM,
// logs "<name>: shutting down...", then calls Shutdown with shutdownTimeout.
// Set certFile and keyFile to enable TLS.
func ServeHTTP(name string, srv *http.Server, shutdownTimeout time.Duration, certFile, keyFile string) {
	go func() {
		var err error
		if certFile != "" {
			err = srv.ListenAndServeTLS(certFile, keyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logging.Errorf("%s: server error: %v", name, err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logging.Infof("%s: shutting down...", name)
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ResolveStr returns flagVal if non-empty, otherwise envVal.
func ResolveStr(flagVal, envVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return envVal
}
