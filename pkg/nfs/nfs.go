package nfs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LINBIT/golinstor/client"
	"github.com/google/go-cmp/cmp"
	log "github.com/sirupsen/logrus"

	"github.com/LINBIT/linstor-gateway/pkg/common"
	"github.com/LINBIT/linstor-gateway/pkg/linstorcontrol"
	"github.com/LINBIT/linstor-gateway/pkg/reactor"
)

const IDFormat = "nfs-%s"
const clusterPrivateVolumeSizeKiB = 64 * 1024 // 64MiB

type NFS struct {
	cli *linstorcontrol.Linstor
}

func New(controllers []string) (*NFS, error) {
	cli, err := linstorcontrol.Default(controllers)
	if err != nil {
		return nil, fmt.Errorf("failed to create linstor client: %w", err)
	}
	return &NFS{cli}, nil
}

func (n *NFS) Get(ctx context.Context, name string) (*ResourceConfig, error) {
	cfg, path, err := reactor.FindConfig(ctx, n.cli.Client, fmt.Sprintf(IDFormat, name))
	if err != nil {
		return nil, fmt.Errorf("failed to check for existing config: %w", err)
	}

	if cfg == nil {
		return nil, nil
	}

	resourceDefinition, volumeDefinitions, resources, err := cfg.DeployedResources(ctx, n.cli.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch existing deployment: %w", err)
	}

	deployedCfg, err := FromPromoter(cfg, resourceDefinition, volumeDefinitions)
	if err != nil {
		return nil, fmt.Errorf("unknown existing reactor config: %w", err)
	}

	deployedCfg.Status = linstorcontrol.StatusFromResources(path, resourceDefinition, resources)

	return deployedCfg, nil
}

func (n *NFS) Create(ctx context.Context, rsc *ResourceConfig) (*ResourceConfig, error) {
	rsc.FillDefaults()

	err := rsc.Valid()
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	configs, _, err := reactor.ListConfigs(ctx, n.cli.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to check for existing NFS configs: %w", err)
	}

	for _, c := range configs {
		if c.ID == rsc.ID() {
			continue
		}
		for _, r := range c.Resources {
			for _, a := range r.Start {
				if a.Type == "ocf:heartbeat:nfsserver" {
					return nil, fmt.Errorf("an NFS config with a different ID already exists: %s", c.ID)
				}
			}
		}
	}

	cfg, path, err := reactor.FindConfig(ctx, n.cli.Client, fmt.Sprintf(IDFormat, rsc.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to check for existing config: %w", err)
	}

	if cfg != nil {
		resourceDefinition, volumeDefinitions, resources, err := cfg.DeployedResources(ctx, n.cli.Client)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch existing deployment: %w", err)
		}

		deployedCfg, err := FromPromoter(cfg, resourceDefinition, volumeDefinitions)
		if err != nil {
			return nil, fmt.Errorf("unknown existing reactor config: %w", err)
		}

		if !rsc.Matches(deployedCfg) {
			log.Debugf("existing resource found that does not match config")
			log.Debugf("diff: %s", cmp.Diff(deployedCfg, rsc))
			return nil, errors.New("resource already exists with incompatible config")
		}

		deployedCfg.Status = linstorcontrol.StatusFromResources(path, resourceDefinition, resources)

		return deployedCfg, nil
	}

	volumes := make([]common.VolumeConfig, len(rsc.Volumes)+1) // +1 for the "cluster private" volume
	volumes[0] = common.VolumeConfig{
		Number:     0,
		SizeKiB:    clusterPrivateVolumeSizeKiB,
		FileSystem: "ext4",
	}
	for i := range rsc.Volumes {
		volumes[i+1] = rsc.Volumes[i].VolumeConfig
	}

	resourceDefinition, deployment, err := n.cli.EnsureResource(ctx, linstorcontrol.Resource{
		Name:          rsc.Name,
		ResourceGroup: rsc.ResourceGroup,
		Volumes:       volumes,
		FileSystem:    "ext4",
	}, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create linstor resource: %w", err)
	}

	cfg, err = rsc.ToPromoter(deployment)
	if err != nil {
		return nil, fmt.Errorf("failed to convert resource to promoter configuration: %w", err)
	}

	err = reactor.EnsureConfig(ctx, n.cli.Client, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to register reactor config file: %w", err)
	}

	_, err = n.Start(ctx, rsc.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to start resources: %w", err)
	}

	rsc.Status = linstorcontrol.StatusFromResources(path, resourceDefinition, deployment)

	return rsc, nil
}

func (n *NFS) Start(ctx context.Context, name string) (*ResourceConfig, error) {
	cfg, _, err := reactor.FindConfig(ctx, n.cli.Client, fmt.Sprintf(IDFormat, name))
	if err != nil {
		return nil, fmt.Errorf("failed to find the resource configuration: %w", err)
	}

	if cfg == nil {
		return nil, nil
	}

	err = reactor.AttachConfig(ctx, n.cli.Client, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to attach reactor configuration: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = common.WaitUntilResourceCondition(waitCtx, n.cli.Client, name, common.AnyResourcesInUse)
	if err != nil {
		return nil, fmt.Errorf("error waiting for resource to become used: %w", err)
	}

	return n.Get(ctx, name)
}

func (n *NFS) Stop(ctx context.Context, name string) (*ResourceConfig, error) {
	cfg, _, err := reactor.FindConfig(ctx, n.cli.Client, fmt.Sprintf(IDFormat, name))
	if err != nil {
		return nil, fmt.Errorf("failed to find the resource configuration: %w", err)
	}

	if cfg == nil {
		return nil, nil
	}

	err = reactor.DetachConfig(ctx, n.cli.Client, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to detach reactor configuration: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = common.WaitUntilResourceCondition(waitCtx, n.cli.Client, name, common.NoResourcesInUse)
	if err != nil {
		return nil, fmt.Errorf("error waiting for resource to become unused: %w", err)
	}

	return n.Get(ctx, name)
}

func (n *NFS) List(ctx context.Context) ([]*ResourceConfig, error) {
	cfgs, paths, err := reactor.ListConfigs(ctx, n.cli.Client)
	if err != nil {
		return nil, err
	}

	result := make([]*ResourceConfig, 0, len(cfgs))
	for i := range cfgs {
		cfg := &cfgs[i]
		path := paths[i]

		var rsc string
		num, _ := fmt.Sscanf(cfg.ID, IDFormat, &rsc)
		if num == 0 {
			log.WithField("id", cfg.ID).Trace("not an NFS resource config, skipping")
			continue
		}

		resourceDefinition, volumeDefinitions, resources, err := cfg.DeployedResources(ctx, n.cli.Client)
		if err != nil {
			log.WithError(err).Warn("failed to fetch deployed resources")
		}

		parsed, err := FromPromoter(cfg, resourceDefinition, volumeDefinitions)
		if err != nil {
			log.WithError(err).Warn("skipping error while parsing promoter config")
			continue
		}

		parsed.Status = linstorcontrol.StatusFromResources(path, resourceDefinition, resources)

		result = append(result, parsed)
	}

	return result, nil
}

func (n *NFS) Delete(ctx context.Context, name string) error {
	err := reactor.DeleteConfig(ctx, n.cli.Client, fmt.Sprintf(IDFormat, name))
	if err != nil {
		return fmt.Errorf("failed to delete reactor config: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = common.WaitUntilResourceCondition(waitCtx, n.cli.Client, name, common.NoResourcesInUse)
	if err != nil {
		return fmt.Errorf("error waiting for resource to become unused: %w", err)
	}

	err = n.cli.ResourceDefinitions.Delete(ctx, name)
	if err != nil && err != client.NotFoundError {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	return nil
}

func (n *NFS) DeleteVolume(ctx context.Context, name string, lun int) (*ResourceConfig, error) {
	cfg, path, err := reactor.FindConfig(ctx, n.cli.Client, fmt.Sprintf(IDFormat, name))
	if err != nil {
		return nil, fmt.Errorf("failed to delete reactor config: %w", err)
	}

	if cfg == nil {
		return nil, nil
	}

	resourceDefinition, volumeDefinition, resources, err := cfg.DeployedResources(ctx, n.cli.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch deployed resources: %w", err)
	}

	rscCfg, err := FromPromoter(cfg, resourceDefinition, volumeDefinition)
	if err != nil {
		return nil, fmt.Errorf("failed to convert volume definition to resource: %w", err)
	}

	status := linstorcontrol.StatusFromResources(path, resourceDefinition, resources)
	if status.Service == common.ServiceStateStarted {
		return nil, errors.New("cannot delete volume while service is running")
	}

	for i := range rscCfg.Volumes {
		if rscCfg.Volumes[i].Number == lun {
			err = n.cli.ResourceDefinitions.DeleteVolumeDefinition(ctx, name, lun)
			if err != nil && err != client.NotFoundError {
				return nil, fmt.Errorf("failed to delete volume definition")
			}

			rscCfg.Volumes = append(rscCfg.Volumes[:i], rscCfg.Volumes[i+1:]...)
			// Manually delete the resources from the current resource config
			for j := range resources {
				resources[j].Volumes = append(resources[j].Volumes[:i], resources[j].Volumes[i+1:]...)
			}

			cfg, err = rscCfg.ToPromoter(resources)
			if err != nil {
				return nil, fmt.Errorf("failed to convert resource to promoter configuration: %w", err)
			}

			err = reactor.EnsureConfig(ctx, n.cli.Client, cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to update config")
			}

			break
		}
	}

	return n.Get(ctx, name)
}
