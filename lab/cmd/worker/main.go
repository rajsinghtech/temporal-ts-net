package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"tailscale.com/tsnet"

	"github.com/temporal-community/temporal-ts-net/lab"
)

const (
	defaultTemporalHost = "temporal-dev:7233"
	defaultMetricsURL   = "http://nagato.keiretsu.ts.net:9100/metrics"
	defaultAIURL        = "http://ai"
	defaultAIModel      = "claude-haiku-4-5"
	taskQueue           = "health-check"
	workflowID          = "health-check"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	configDir, err := os.UserConfigDir()
	if err != nil {
		logger.Error("UserConfigDir", "err", err)
		os.Exit(1)
	}

	tsNode := &tsnet.Server{
		Hostname: "lab-worker",
		Dir:      filepath.Join(configDir, "lab-worker"),
		AuthKey:  mustEnv(logger, "TS_AUTHKEY"),
	}
	if err := tsNode.Start(); err != nil {
		logger.Error("tsnet start", "err", err)
		os.Exit(1)
	}
	defer tsNode.Close()

	upCtx, upCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer upCancel()
	if _, err := tsNode.Up(upCtx); err != nil {
		logger.Error("tsnet up", "err", err)
		os.Exit(1)
	}
	logger.Info("joined tailnet", "hostname", "lab-worker")

	temporalHost := envOr("TEMPORAL_HOST", defaultTemporalHost)
	c, err := client.Dial(client.Options{
		HostPort: temporalHost,
		Logger:   logger,
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
					// Use regular dial for localhost; tsnet for tailnet hostnames
					host, _, _ := net.SplitHostPort(addr)
					if host == "localhost" || host == "127.0.0.1" {
						return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
					}
					return tsNode.Dial(ctx, "tcp", addr)
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithKeepaliveParams(keepalive.ClientParameters{
					Time:                30 * time.Second,
					Timeout:             10 * time.Second,
					PermitWithoutStream: true,
				}),
			},
		},
	})
	if err != nil {
		logger.Error("temporal dial", "err", err)
		os.Exit(1)
	}
	defer c.Close()
	logger.Info("connected to temporal", "host", temporalHost)

	metricsURL := envOr("METRICS_URL", defaultMetricsURL)
	acts := lab.NewActivities(
		tsNode.HTTPClient(),
		metricsURL,
		envOr("AI_URL", defaultAIURL),
		envOr("AI_MODEL", defaultAIModel),
	)

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if sample, err := acts.FetchMetrics(pingCtx); err != nil {
		logger.Warn("metrics endpoint unreachable", "url", metricsURL, "err", err)
	} else {
		logger.Info("metrics reachable", "url", metricsURL, "sample", strings.SplitN(sample, "\n", 2)[0])
	}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(lab.HealthCheckWorkflow)
	w.RegisterActivity(acts)

	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startCancel()
	_, startErr := c.ExecuteWorkflow(startCtx, client.StartWorkflowOptions{
		ID:           workflowID,
		TaskQueue:    taskQueue,
		CronSchedule: "* * * * *",
	}, lab.HealthCheckWorkflow)
	if startErr != nil {
		// A non-nil error here usually means the cron workflow is already running — fine to continue
		logger.Warn("start workflow (may already be running)", "id", workflowID, "err", startErr)
	} else {
		logger.Info("started cron workflow", "id", workflowID, "schedule", "* * * * *")
	}

	logger.Info("worker running", "taskQueue", taskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		logger.Error("worker stopped with error", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(logger *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		logger.Error("required env var not set", "key", key)
		os.Exit(1)
	}
	return v
}
