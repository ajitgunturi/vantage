// cmd/mq is the entrypoint for the MQ microservice.
// It wires configuration, the in-memory ring-buffer store, the gRPC data plane
// (Produce + Consume), and the HTTP control-plane (GET /api/v1/queue/inspect)
// into a single process. Both servers are managed by an errgroup; SIGTERM/SIGINT
// triggers graceful shutdown in the correct order.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/ajitg/vantage/internal/config"
	mqhttp "github.com/ajitg/vantage/internal/http"
	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	pkglogger "github.com/ajitg/vantage/pkg/logger"
	"github.com/ajitg/vantage/pkg/pb"
)

func main() {
	l := pkglogger.New("mq")
	slog.SetDefault(l)

	cfg := config.FromEnv()

	s := queue.NewBroker(queue.BrokerConfig{
		Capacity:         cfg.BufferSize,
		Policy:           queue.OverflowPolicy(cfg.OverflowPolicy),
		BlockTimeout:     time.Duration(cfg.BlockTimeoutMS) * time.Millisecond,
		MaxDeliveries:    uint32(cfg.MaxDeliveries),
		DLQCapacity:      cfg.DLQCapacity,
		RetryBackoffBase: time.Duration(cfg.RetryBackoffBaseMS) * time.Millisecond,
		RetryBackoffMax:  time.Duration(cfg.RetryBackoffMaxMS) * time.Millisecond,
		LeaseTTL:         time.Duration(cfg.LeaseTTLMS) * time.Millisecond,
	})
	mqSrv := server.NewMQServer(s, cfg.ConsumeCredit)
	defer mqSrv.Shutdown()

	// gRPC server with keepalive options for long-lived Consume streams.
	// Keepalive parameters prevent silent stream death behind NAT/Kubernetes kube-proxy
	// (IPVS 350s idle timeout) per CONTEXT.md Transport decision.
	grpcSrv := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              30 * time.Second,
			Timeout:           10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             15 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterMQServiceServer(grpcSrv, mqSrv)

	// HTTP control-plane: method-scoped route requires Go 1.22+ net/http ServeMux.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/queue/inspect", mqhttp.InspectHandler(mqSrv))
	mux.HandleFunc("GET /api/v1/queue/dlq", mqhttp.DLQHandler(mqSrv))
	mux.HandleFunc("POST /api/v1/queue/dlq/replay", mqhttp.DLQReplayHandler(mqSrv))
	mux.HandleFunc("GET /healthz", mqhttp.HealthzHandler())
	mux.HandleFunc("GET /readyz", mqhttp.ReadyzHandler(mqSrv))
	mux.HandleFunc("GET /metrics", mqhttp.MetricsHandler(mqSrv))
	httpSrv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGTERM or SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	g, gctx := errgroup.WithContext(ctx)

	// (1) gRPC server goroutine.
	g.Go(func() error {
		grpcLis, err := net.Listen("tcp", cfg.GRPCAddr)
		if err != nil {
			return err
		}
		slog.Info("gRPC listening", "addr", cfg.GRPCAddr)
		return grpcSrv.Serve(grpcLis)
	})

	// (2) HTTP server goroutine.
	g.Go(func() error {
		slog.Info("HTTP listening", "addr", cfg.HTTPAddr)
		return httpSrv.ListenAndServe()
	})

	// (3) Shutdown coordination goroutine — waits for signal then tears down in order.
	g.Go(func() error {
		<-gctx.Done()
		// preStop drain: stop accepting Produce (readiness gate flips via
		// IsShuttingDown → the Service stops routing new producers) but keep
		// Consume streams alive so healthy consumers ack the backlog. This
		// shrinks the rollout loss window to ~zero when consumers are healthy;
		// what remains at the deadline is bounded by MQ_DRAIN_TIMEOUT_MS.
		mqSrv.BeginDrain()
		drainDeadline := time.After(time.Duration(cfg.DrainTimeoutMS) * time.Millisecond)
		drainTick := time.NewTicker(100 * time.Millisecond)
		slog.Info("drain started — refusing Produce, waiting for consumers to clear backlog",
			"timeout_ms", cfg.DrainTimeoutMS)
		// Stall detection: draining only makes sense while consumers can make
		// progress. With zero connected consumers and no acks for 2s straight,
		// nothing can clear the backlog — exit promptly instead of holding the
		// ports for the full drain window (a brief idle grace still lets a
		// collector in its reconnect backoff reattach).
		const stallTicks = 20 // 20 × 100ms = 2s of no-consumer, no-progress
		idleTicks := 0
		lastConsumed := int64(-1)
	drain:
		for {
			select {
			case <-drainTick.C:
				if mqSrv.Drained() {
					slog.Info("drain complete — no queued or in-flight messages")
					break drain
				}
				st := mqSrv.Stats()
				if st.ActiveConsumers == 0 && st.Consumed == lastConsumed {
					idleTicks++
					if idleTicks >= stallTicks {
						slog.Warn("drain stalled — no consumers connected and no progress; exiting with messages remaining",
							"depth", st.Depth, "retry_depth", st.RetryDepth, "in_flight", st.InFlight)
						break drain
					}
				} else {
					idleTicks = 0
				}
				lastConsumed = st.Consumed
			case <-drainDeadline:
				st := mqSrv.Stats()
				slog.Warn("drain timeout — proceeding to shutdown with messages remaining",
					"depth", st.Depth, "retry_depth", st.RetryDepth, "in_flight", st.InFlight)
				break drain
			}
		}
		drainTick.Stop()
		// Close shutdownCh so Consume send loops wake from their blocking
		// selects and return codes.Unavailable before GracefulStop polls them.
		mqSrv.Shutdown()
		// Race GracefulStop against a 5s timeout; fall back to Stop() so a slow
		// stream cannot prevent the process from exiting (M-3 fix).
		gracefulDone := make(chan struct{})
		go func() {
			grpcSrv.GracefulStop()
			close(gracefulDone)
		}()
		select {
		case <-gracefulDone:
			// all streams finished cleanly
		case <-time.After(5 * time.Second):
			slog.Warn("GracefulStop timeout — forcing Stop()")
			grpcSrv.Stop()
			<-gracefulDone // Stop() wakes GracefulStop's condition variable
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	})

	slog.Info("startup complete", "grpc_addr", cfg.GRPCAddr, "http_addr", cfg.HTTPAddr, "buffer", cfg.BufferSize)

	if err := g.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
