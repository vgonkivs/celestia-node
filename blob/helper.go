package blob

import (
	"fmt"
	"sort"

	libshare "github.com/celestiaorg/go-square/v2/share"
)

// BlobsToShares accepts blobs and convert them to the Shares.
func BlobsToShares(nodeBlobs ...*Blob) ([]libshare.Share, error) {
	sort.Slice(nodeBlobs, func(i, j int) bool {
		return nodeBlobs[i].Blob.Namespace().IsLessThan(nodeBlobs[j].Blob.Namespace())
	})

	splitter := libshare.NewSparseShareSplitter()
	for i, nodeBlob := range nodeBlobs {
		err := splitter.Write(nodeBlob.Blob)
		if err != nil {
			return nil, fmt.Errorf("failed to split blob at index: %d: %w", i, err)
		}
	}
	return splitter.Export(), nil
}

// ToLibBlobs converts node's blob type to the blob type from go-square.
func ToLibBlobs(blobs ...*Blob) []*libshare.Blob {
	libBlobs := make([]*libshare.Blob, len(blobs))
	for i := range blobs {
		libBlobs[i] = blobs[i].Blob
	}
	return libBlobs
}

func calculateIndex(rowLength, blobIndex int) (row, col int) {
	row = blobIndex / rowLength
	col = blobIndex - (row * rowLength)
	return
}
