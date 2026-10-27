package packagehost

import (
	"context"
	"encoding/json"
	"io/fs"

	"github.com/Cyenoch/cordis/contract"
)

// MetadataDecoder projects a host-owned manifest and its signed contribution
// documents into framework metadata. It must not execute plugin code. Files are
// an already bounded, link-free private archive snapshot, never a mutable source.
type MetadataDecoder func(context.Context, json.RawMessage, fs.FS) (contract.Manifest, error)

type Options struct {
	Trust  TrustPolicy
	Limits Limits
	Decode MetadataDecoder
}

func decodeMetadata(ctx context.Context, raw []byte, files fs.FS, decoder MetadataDecoder) (contract.Manifest, error) {
	if decoder == nil {
		return decodeManifest(raw)
	}
	if err := contract.CheckJSON(raw, 32, 10000); err != nil {
		return contract.Manifest{}, err
	}
	manifest, err := decoder(ctx, raw, files)
	if err != nil {
		return contract.Manifest{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return contract.Manifest{}, err
	}
	return manifest, nil
}
