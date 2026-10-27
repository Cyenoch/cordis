package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

// failedVersionRecovery uses the same host and unchanged entry configuration
// throughout. The 1.2.0 archive must contain a separately built chain artifact
// with -ldflags '-X main.rejectInitialization=true', not a repackaged healthy binary.
// Neither replacement confirms a migration or changes the package data version.
func failedVersionRecovery(
	ctx context.Context,
	options cordis.Options,
	healthyArchive string,
	badArchive string,
) (resultErr error) {
	host, err := cordis.Open(options)
	if err != nil {
		return fmt.Errorf("open version recovery catalog: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, host.Close(closeCtx))
	}()

	healthy, err := host.Install(ctx, healthyArchive)
	if err != nil {
		return fmt.Errorf("install healthy version: %w", err)
	}
	if healthy.Ref.ID != "example.chain" || healthy.Ref.Version != "1.0.0" {
		return fmt.Errorf("unexpected healthy package: %+v", healthy.Ref)
	}
	if err := host.Select(ctx, healthy.Ref); err != nil {
		return fmt.Errorf("select healthy version: %w", err)
	}
	spec := entry("version-recovery", "provider", "", "")
	instance, err := host.Enable(ctx, spec)
	if err != nil {
		return fmt.Errorf("enable healthy version: %w", err)
	}
	if err := instance.Wait(ctx); err != nil {
		return fmt.Errorf("start healthy version: %w", err)
	}
	before := instance.Status()
	if before.State != contract.Active || before.Identity.Digest != healthy.Ref.Digest {
		return fmt.Errorf("healthy version is not active: %+v", before)
	}
	if err := expectRecoveryProvider(ctx, host); err != nil {
		return fmt.Errorf("call initial healthy version: %w", err)
	}

	broken, err := host.Install(ctx, badArchive)
	if err != nil {
		return fmt.Errorf("install broken version: %w", err)
	}
	if broken.Ref.ID != healthy.Ref.ID || broken.Ref.Version != "1.2.0" {
		return fmt.Errorf("unexpected broken package: %+v", broken.Ref)
	}
	if broken.Ref.Digest == healthy.Ref.Digest || broken.Manifest.DataVersion != healthy.Manifest.DataVersion {
		return errors.New("version recovery requires distinct code packages with unchanged data version")
	}
	const failure = "intentional version-specific initialization failure"
	if err := host.Replace(ctx, broken.Ref, packagehost.ChangeOptions{}); err == nil {
		return errors.New("broken signed version unexpectedly started")
	} else if !strings.Contains(err.Error(), failure) {
		return fmt.Errorf("broken version did not report its initialization failure: %w", err)
	}
	failed, err := host.Status(spec.ID)
	if err != nil {
		return fmt.Errorf("inspect broken version: %w", err)
	}
	if failed.State != contract.Failed || failed.Identity.Digest != broken.Ref.Digest {
		return fmt.Errorf("broken version did not remain failed: %+v", failed)
	}
	if !strings.Contains(failed.Error, failure) {
		return fmt.Errorf("failed status lost version-specific diagnostic: %q", failed.Error)
	}
	selected, err := host.Store().Selected(healthy.Ref.ID)
	if err != nil {
		return fmt.Errorf("inspect failed selection: %w", err)
	}
	if selected.Ref != broken.Ref {
		return fmt.Errorf("failed startup silently rolled selection back: %+v", selected.Ref)
	}
	if err := host.Access().Check(before.Identity); err == nil {
		return errors.New("failed replacement retained old activation authority")
	}
	if _, err := invoke(ctx, host, "", "chain.a", "stats"); err == nil {
		return errors.New("failed version remained callable")
	}

	// A historical initialization error must remain observable, without preventing
	// the explicit old-code selection from recreating the provider in this host.
	if err := host.Replace(ctx, healthy.Ref, packagehost.ChangeOptions{}); err == nil {
		return errors.New("recovery replacement omitted historical initialization failure")
	} else if !strings.Contains(err.Error(), failure) {
		return fmt.Errorf("recovery replacement lost historical initialization failure: %w", err)
	}
	after, err := host.Status(spec.ID)
	if err != nil {
		return fmt.Errorf("inspect recovered version: %w", err)
	}
	if after.State != contract.Active || after.Identity.Digest != healthy.Ref.Digest {
		return fmt.Errorf("old signed code did not recover in the live host: %+v", after)
	}
	if after.Identity.Activation == "" || after.Identity.Activation == before.Identity.Activation {
		return errors.New("old-code recovery reused a retired activation")
	}
	selected, err = host.Store().Selected(healthy.Ref.ID)
	if err != nil {
		return fmt.Errorf("inspect recovered selection: %w", err)
	}
	if selected.Ref != healthy.Ref {
		return fmt.Errorf("recovery did not select old signed package: %+v", selected.Ref)
	}
	entries, err := host.Store().Entries()
	if err != nil {
		return fmt.Errorf("inspect recovered configuration: %w", err)
	}
	if len(entries) != 1 || entries[0].ID != spec.ID || string(entries[0].Config) != string(spec.Config) {
		return fmt.Errorf("version recovery changed persistent entry configuration: %+v", entries)
	}
	if err := expectRecoveryProvider(ctx, host); err != nil {
		return fmt.Errorf("call recovered old version: %w", err)
	}
	return nil
}

func expectRecoveryProvider(ctx context.Context, host *cordis.Host) error {
	value, err := invoke(ctx, host, "", "chain.a", "stats")
	if err != nil {
		return err
	}
	if value.Label != "initial" || value.Acquired != 0 || value.Released != 0 || value.Live != 0 {
		return fmt.Errorf("unexpected version recovery provider output: %+v", value)
	}
	return nil
}
