package cli

import (
	"context"
	"flag"
	"net/url"

	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/spf13/afero"

	"github.com/xenitab/spegel/pkg/oci"
)

// ConfigurationConfig provides configuration for the configuration subcommand.
type ConfigurationConfig struct {
	ContainerdRegistryConfigPath string
	Registries                   []url.URL
	MirrorRegistries             []url.URL
	ResolveTags                  bool

	registriesValue       urlsValue
	mirrorRegistriesValue urlsValue
}

func newConfigurationCommand(config *ConfigurationConfig) *ffcli.Command {
	fs := flag.NewFlagSet("configuration", flag.ExitOnError)

	fs.StringVar(&config.ContainerdRegistryConfigPath, "containerd-registry-config-path", "/etc/containerd/certs.d", "Directory where mirror configuration is written.")
	fs.Var(&config.registriesValue, "registries", "Registries that are configured to be mirrored.")
	fs.Var(&config.mirrorRegistriesValue, "mirror-registries", "Registries that are configured to act as mirrored.")
	fs.BoolVar(&config.ResolveTags, "resolve-tags", true, "When true Spegel will resolve tags to digests.")

	return &ffcli.Command{
		Name:       "configuration",
		ShortUsage: "spegel configuration [flags]",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			return configurationExec(ctx, config)
		},
		Options: []ff.Option{
			ff.WithEnvVars(),
		},
	}
}

func configurationExec(ctx context.Context, config *ConfigurationConfig) error {
	fs := afero.NewOsFs()
	err := oci.AddMirrorConfiguration(ctx, fs, config.ContainerdRegistryConfigPath, config.Registries, config.MirrorRegistries, config.ResolveTags)
	if err != nil {
		return err
	}
	return nil
}
