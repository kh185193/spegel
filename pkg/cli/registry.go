package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-logr/logr"
	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	pkgkubernetes "github.com/xenitab/pkg/kubernetes"
	"golang.org/x/sync/errgroup"

	"github.com/xenitab/spegel/pkg/metrics"
	"github.com/xenitab/spegel/pkg/oci"
	"github.com/xenitab/spegel/pkg/registry"
	"github.com/xenitab/spegel/pkg/routing"
	"github.com/xenitab/spegel/pkg/state"
	"github.com/xenitab/spegel/pkg/throttle"
)

// RegistryConfig provides configuration for the registry subcommand.
type RegistryConfig struct {
	BootstrapConfig
	BlobSpeed                    *throttle.Byterate
	ContainerdRegistryConfigPath string
	MetricsAddr                  string
	LocalAddr                    string
	ContainerdSock               string
	ContainerdNamespace          string
	RouterAddr                   string
	RegistryAddr                 string
	Registries                   []url.URL
	MirrorResolveTimeout         time.Duration
	MirrorResolveRetries         int
	ResolveLatestTag             bool

	blobSpeedValue  int
	registriesValue urlsValue
}

// BoostrapConfig provides boostrap configuration for the registry subcommand.
type BootstrapConfig struct {
	BootstrapKind           string
	HTTPBootstrapAddr       string
	HTTPBootstrapPeer       string
	KubeconfigPath          string
	LeaderElectionName      string
	LeaderElectionNamespace string
}

func newRegistryCommand(config *RegistryConfig) *ffcli.Command {
	fs := flag.NewFlagSet("registry", flag.ExitOnError)

	// TODO: this type is wrong, should it be string?
	fs.IntVar(&config.blobSpeedValue, "blob-speed", 0, "Maximum write speed per request when serving blob layers. Should be an integer followed by unit Bps, KBps, MBps, GBps, or TBps.")
	fs.StringVar(&config.ContainerdRegistryConfigPath, "containerd-registry-config-path", "", "Directory where mirror configuration is written.")
	fs.StringVar(&config.MetricsAddr, "metrics-addr", "", "Address to serve metrics.")
	fs.StringVar(&config.LocalAddr, "local-addr", "", "Address that the local Spegel instance will be reached at.")
	fs.StringVar(&config.ContainerdSock, "containerd-sock", "/run/containerd/containerd.sock", "Endpoint of containerd service.")
	fs.StringVar(&config.ContainerdNamespace, "containerd-namespace", "k8s.io", "Containerd namespace to fetch images from.")
	fs.StringVar(&config.RouterAddr, "router-addr", "", "Address to serve router.")
	fs.StringVar(&config.RegistryAddr, "registry-addr", "", "Address to server image registry.")
	fs.Var(&config.registriesValue, "registries", "Registries that are configured to be mirrored.")
	fs.DurationVar(&config.MirrorResolveTimeout, "mirror-resolve-timeout", 5*time.Second, "Max duration spent finding a mirror.")
	fs.IntVar(&config.MirrorResolveRetries, "mirror-resolve-retries", 3, "Max amount of mirrors to attempt.")
	fs.BoolVar(&config.ResolveLatestTag, "resolve-latest-tag", true, "When true latest tags will be resolved to digests.")

	fs.StringVar(&config.BootstrapKind, "boostrap-kind", "", "Kind of bootsrapper to use.")
	fs.StringVar(&config.HTTPBootstrapAddr, "http-bootstrap-addr", "", "Address to serve for HTTP bootstrap.")
	fs.StringVar(&config.HTTPBootstrapPeer, "http-bootstrap-peer", "", "Peer to HTTP bootstrap with.")
	fs.StringVar(&config.KubeconfigPath, "kubeconfig-path", "", "Path to the kubeconfig file.")
	fs.StringVar(&config.LeaderElectionName, "leader-election-name", "spegel-leader-election", "Name of the leader election.")
	fs.StringVar(&config.LeaderElectionNamespace, "leader-election-namespace", "spegel", "Kubernetes namespace to write leader election data.")

	return &ffcli.Command{
		Name:       "registry",
		ShortUsage: "spegel registry [flags]",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			return registryExec(ctx, config)
		},
		Options: []ff.Option{
			ff.WithEnvVars(),
		},
	}
}

func registryExec(ctx context.Context, config *RegistryConfig) (err error) {
	log := logr.FromContextOrDiscard(ctx)
	g, ctx := errgroup.WithContext(ctx)

	// OCI Client
	ociClient, err := oci.NewContainerd(config.ContainerdSock, config.ContainerdNamespace, config.ContainerdRegistryConfigPath, config.Registries)
	if err != nil {
		return err
	}
	err = ociClient.Verify(ctx)
	if err != nil {
		return err
	}

	// Metrics
	metrics.Register()
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.DefaultGatherer, promhttp.HandlerOpts{}))
	metricsSrv := &http.Server{
		Addr:    config.MetricsAddr,
		Handler: mux,
	}
	g.Go(func() error {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return metricsSrv.Shutdown(shutdownCtx)
	})

	// Router
	_, registryPort, err := net.SplitHostPort(config.RegistryAddr)
	if err != nil {
		return err
	}
	bootstrapper, err := getBootstrapper(config.BootstrapConfig)
	if err != nil {
		return err
	}
	router, err := routing.NewP2PRouter(ctx, config.RouterAddr, bootstrapper, registryPort)
	if err != nil {
		return err
	}
	g.Go(func() error {
		return router.Run(ctx)
	})
	g.Go(func() error {
		<-ctx.Done()
		return router.Close()
	})

	// State tracking
	g.Go(func() error {
		err := state.Track(ctx, ociClient, router, config.ResolveLatestTag)
		if err != nil {
			return err
		}
		return nil
	})

	// Registry
	registryOpts := []registry.Option{
		registry.WithResolveLatestTag(config.ResolveLatestTag),
		registry.WithResolveRetries(config.MirrorResolveRetries),
		registry.WithResolveTimeout(config.MirrorResolveTimeout),
		registry.WithLocalAddress(config.LocalAddr),
	}
	if config.BlobSpeed != nil {
		registryOpts = append(registryOpts, registry.WithBlobSpeed(*config.BlobSpeed))
	}
	reg := registry.NewRegistry(ociClient, router, registryOpts...)
	regSrv := reg.Server(config.RegistryAddr, log)
	g.Go(func() error {
		if err := regSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return regSrv.Shutdown(shutdownCtx)
	})

	log.Info("running Spegel", "registry", config.RegistryAddr, "router", config.RouterAddr)
	err = g.Wait()
	if err != nil {
		return err
	}
	return nil
}

func getBootstrapper(config BootstrapConfig) (routing.Bootstrapper, error) {
	switch config.BootstrapKind {
	case "http":
		return routing.NewHTTPBootstrapper(config.HTTPBootstrapAddr, config.HTTPBootstrapPeer), nil
	case "kubernetes":
		cs, err := pkgkubernetes.GetKubernetesClientset(config.KubeconfigPath)
		if err != nil {
			return nil, err
		}
		return routing.NewKubernetesBootstrapper(cs, config.LeaderElectionNamespace, config.LeaderElectionName), nil
	default:
		return nil, fmt.Errorf("unknown bootstrap kind %s", config.BootstrapKind)
	}
}
