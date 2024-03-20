package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/xenitab/spegel/pkg/throttle"
)

type Command struct {
	Configuration *ConfigurationConfig
	Registry      *RegistryConfig

	cmd *ffcli.Command
}

func New() *Command {
	configurationConfig := &ConfigurationConfig{}
	registryConfig := &RegistryConfig{}

	return &Command{
		Configuration: configurationConfig,
		Registry:      registryConfig,
		cmd: &ffcli.Command{
			ShortUsage: "spegel <subcommand>",
			Subcommands: []*ffcli.Command{
				newConfigurationCommand(configurationConfig),
				newRegistryCommand(registryConfig),
			},
		},
	}
}

func (c *Command) Parse(args []string) error {
	if err := c.cmd.Parse(args); err != nil {
		return err
	}

	// update non-standard type fields
	c.Configuration.Registries = c.Configuration.registriesValue.URLs
	c.Configuration.MirrorRegistries = c.Configuration.mirrorRegistriesValue.URLs
	c.Registry.BlobSpeed = (*throttle.Byterate)(&c.Registry.blobSpeedValue)
	c.Registry.Registries = c.Registry.registriesValue.URLs

	return nil
}

func (c *Command) Run(ctx context.Context) error {
	return c.cmd.Run(ctx)
}

// urlsValue is used to parse command line arguments to []url.URL fields.
type urlsValue struct {
	URLs []url.URL
}

func (v *urlsValue) String() string {
	strs := []string{}
	for _, url := range v.URLs {
		strs = append(strs, url.String())
	}
	return strings.Join(strs, ",")
}

func (v *urlsValue) Set(s string) error {
	urls := []url.URL{}
	for _, str := range strings.Split(s, ",") {
		url, err := url.Parse(str)
		if err != nil {
			return fmt.Errorf("failed to parse URLs from '%s': %w", s, err)
		}
		urls = append(urls, *url)
	}
	v.URLs = urls
	return nil
}
