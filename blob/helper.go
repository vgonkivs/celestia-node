package blob

import (
	"bytes"
	"sort"

	"github.com/tendermint/tendermint/types"

	"github.com/celestiaorg/celestia-app/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/pkg/shares"

	"github.com/celestiaorg/celestia-node/share"
)

// SharesToBlobs takes raw shares and converts them to the blobs.
func SharesToBlobs(rawShares []share.Share) ([]*Blob, error) {
	if len(rawShares) == 0 {
		return nil, ErrBlobNotFound
	}
	rawShares, err := removePadding(rawShares)
	if err != nil {
		return nil, err
	}

	shareSequences, err := shares.ParseShares(rawShares)
	if err != nil {
		return nil, err
	}

	blobs := make([]*Blob, len(shareSequences))
	for i, sequence := range shareSequences {
		data, err := sequence.RawData()
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}

		blob, err := NewBlob(data[0], sequence.NamespaceID, data)
		if err != nil {
			return nil, err
		}
		blobs[i] = blob
	}
	return blobs, nil
}

// BlobsToShares accepts blobs and convert them to the Shares.
func BlobsToShares(blobs ...*Blob) ([]share.Share, error) {
	b := make([]types.Blob, len(blobs))
	for i, blob := range blobs {
		b[i] = types.Blob{
			NamespaceID:  blob.NamespaceID(),
			Data:         blob.Data(),
			ShareVersion: uint8(blob.Version()),
		}
	}

	sort.Slice(b, func(i, j int) bool {
		val := bytes.Compare(b[i].NamespaceID, b[j].NamespaceID)
		return val <= 0
	})

	rawShares, err := shares.SplitBlobs(0, nil, b, false)
	if err != nil {
		return nil, err
	}
	return shares.ToBytes(rawShares), nil
}

const (
	perByteGasTolerance = 2
	pfbGasFixedCost     = 80000
)

// estimateGas estimates the gas required to pay for a set of blobs in a PFB.
func estimateGas(blobs ...*Blob) uint64 {
	totalByteCount := 0
	for _, blob := range blobs {
		totalByteCount += len(blob.Data()) + appconsts.NamespaceSize
	}
	variableGasAmount := (appconsts.DefaultGasPerBlobByte + perByteGasTolerance) * totalByteCount

	return uint64(variableGasAmount + pfbGasFixedCost)
}

// constructAndVerifyBlob reconstruct a Blob from the passed shares and compares commitments.
func constructAndVerifyBlob(sh []share.Share, commitment Commitment) (*Blob, bool, error) {
	blob, err := SharesToBlobs(sh)
	if err != nil {
		return nil, false, err
	}

	equal := blob[0].Commitment().Equal(commitment)
	return blob[0], equal, nil
}

// removePadding ensures that namespace padding shares will not be included in the blob creation
// as they are not the part of the blob
// TODO(@vgonkivs): remove after https://github.com/celestiaorg/celestia-node/pull/1999 will be
// merged
func removePadding(rawShares [][]byte) ([][]byte, error) {
	newShares := make([][]byte, 0)
	for _, sh := range rawShares {
		bShare, err := shares.NewShare(sh)
		if err != nil {
			return nil, err
		}

		isPadding, err := bShare.IsPadding()
		if err != nil {
			return nil, err
		}
		if !isPadding {
			newShares = append(newShares, sh)
		}
	}
	return newShares, nil
}